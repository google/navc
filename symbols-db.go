/*
 * Copyright 2015 Google Inc. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"crypto/sha1"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/go-clang/v3.6/clang"
)

/*
 * The symbols database keeps all the index of the code. We keep one file (or
 * database file) per indexed C file. This file will contain the information of
 * the code in the C file and all the headers included recursively (the headers
 * included in the headers). We call this translation unit or TU (following
 * clang's nomenclature). Having a database per translation unit will allow
 * greater parallelism and higher performance at indexing time. All these files
 * are stored in the symbols directory, .navc_dbsymbols by default, and
 * represented by the struct symbolsDB. In the symbols directory, each database
 * file uses the sha1 of the original file name as file name. The file will
 * simply have a serialized form of some of the fields in the structure
 * symbolsTUDB. This structure has the following fields:
 *
 * - File: Name of the source file indexed.
 *
 * - Mtime: Modification time of the file when was indexed.
 *
 * - Headers (fileID -> Time): Contains all the header files included in the
 * translation unit and the modification time of each when the translation unit
 * was indexed.
 *
 * - SymLoc (symbolLoc -> symbolID): Contains all the symbols uses in the
 * translatio unit. It maps symbol locations to symbol ID.
 *
 * - SymData (symbolID -> symbolData): Contains the data of all the symbols
 * indexed by symbol ID. Given any symbol location, we can find its symbol data
 * in the translation unit. The symbol data will have the list of declarations
 * of the symbol and the list of uses in the translation unit. If the definition
 * of the symbol is available in this translation unit, DefAvail will be true
 * and Def will hold the location of the definition.
 *
 * - Includers: In case the translation unit represent a header file, this list
 * will have all the translation units including this file. This is the only
 * information necessary for header files. Header files is where two translation
 * units meet. For instance, one function declared in a.h and used by a.c can be
 * defined in b.c. The meeting point of a.c and b.c is their included header
 * file a.h. Keeping this information is not really necessary but it speed up
 * lookup of symbols. In theory, these can be recreated from the regular
 * translation units.
 *
 * fileID and symbolID are simply a hash of the name of the file or symbol. In
 * this case, it is the sha1 hash of the names.
 *
 * symbolsDB has a map with an entry for every symbolsTUDB. On each entry, it
 * caches some information of the translation unit. Translations units are
 * inserted in the InsertTUDB function. This function will also be called to
 * replace an old translation unit of a file. Translation units will be
 * persisted to disk whenever the symbolsDB is flushed. This is done by calling
 * the FlushDB function.
 */

type symbolID [sha1.Size]byte
type fileID [sha1.Size]byte

type symbolLoc struct {
	File fileID
	Line int16
	Col  int16
}

type symbolUse struct {
	Loc      symbolLoc
	FuncCall bool
}

type symbolData struct {
	Name     string
	Uses     []symbolUse
	Decls    []symbolLoc
	DefAvail bool
	Def      symbolLoc
}

// SymbolLocReq is used as input and output structure for the daemon requests.
type SymbolLocReq struct {
	File string
	Line int
	Col  int
}

type symbolInfo struct {
	name string
	usr  string
	loc  SymbolLocReq
}

type symbolsTUDB struct {
	File string

	// .c data
	Mtime   time.Time
	SymLoc  map[symbolLoc]symbolID
	SymData map[symbolID]symbolData
	Headers map[fileID]time.Time

	// .h lists
	Includers map[fileID]bool

	// used only while parsing
	headersTUDB map[string]bool
	tmpFile     string
}

type tuSymbolsDBCache struct {
	tudb  *symbolsTUDB
	Mtime time.Time
	Path  string

	accTime time.Time
	dirty   bool
}

type symbolsDB struct {
	TUDBs map[fileID]*tuSymbolsDBCache
}

// db directory path
var dbDirPath string

// db temp directory = dbDirPath + "/tmp"
var dbDirTmp string

// db index file = dbDirPath + "/index"
var dbDirIndex string

///// Helper functions

func getStringEncode(str string) [sha1.Size]byte {
	return sha1.Sum([]byte(str))
}

func nonExistingHeaderName(headPath string) string {
	// adding magic to filename to not confuse it with real files
	return "IDoNotReallyExist-" + filepath.Base(headPath)
}

///// Symbols DB methods

func newSymbolsDB(dbDirPathIn string) *symbolsDB {
	// create index directory if it does not exist
	err := os.MkdirAll(dbDirPathIn+"/tmp", 0700)
	if err != nil {
		log.Panic("unable to create db dir ", err)
	}
	dbDirPath = dbDirPathIn
	dbDirTmp = dbDirPath + "/tmp"
	dbDirIndex = dbDirPath + "/index"

	newDB, err := loadSymbolsDBIndex()
	if err != nil && os.IsNotExist(err) {
		newDB = &symbolsDB{make(map[fileID]*tuSymbolsDBCache)}
	} else if err != nil {
		return nil
	}

	return newDB
}

func loadSymbolsDBIndex() (*symbolsDB, error) {
	var index symbolsDB

	indexFile, err := os.Open(dbDirIndex)
	if err != nil {
		return nil, err
	}
	defer indexFile.Close()

	dec := gob.NewDecoder(indexFile)

	err = dec.Decode(&index)
	if err != nil {
		return nil, err
	}

	return &index, nil
}

func (db *symbolsDB) saveSymbolsDBIndex() error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	indexFile, err := os.OpenFile(dbDirIndex, flags, 0644)
	if err != nil {
		return err
	}
	defer indexFile.Close()

	enc := gob.NewEncoder(indexFile)

	err = enc.Encode(db)
	if err != nil {
		return err
	}

	return nil
}

func (db *symbolsDB) FlushDB(saveFrom time.Time) error {
	for _, cache := range db.TUDBs {
		if cache.tudb == nil {
			continue
		}

		if cache.accTime.After(saveFrom) {
			continue
		}

		if cache.dirty {
			err := cache.tudb.SaveSymbolsTUDB(getDBFileName(cache.Path))
			if err != nil {
				return err
			}
		}
		cache.dirty = false
		cache.tudb = nil
	}

	db.saveSymbolsDBIndex()

	return nil
}

func getDBFileNameFromSha1(fid fileID) string {
	return dbDirPath + "/" + hex.EncodeToString(fid[:])
}

func getDBFileName(file string) string {
	return getDBFileNameFromSha1(getStringEncode(file))
}

func (db *symbolsDB) FileExist(filePath string) bool {
	return db.TUDBs[getStringEncode(filePath)] != nil
}

func (db *symbolsDB) LoadSymbolsTUDBFromSha1(file fileID) (*symbolsTUDB, error) {
	tudb, err := loadSymbolsTUDB(getDBFileNameFromSha1(file))
	if err != nil {
		return nil, err
	}

	return tudb, nil
}

func (db *symbolsDB) getListOfFilenames(fileSet map[fileID]bool) []string {
	filenames := []string{}
	for fid := range fileSet {
		filenames = append(filenames, db.TUDBs[fid].Path)
	}

	return filenames
}

func (db *symbolsDB) GetIncluders(headPath string) ([]string, error) {
	realHeader := true
	hmtime := time.Time{}
	headID := getStringEncode(headPath)

	if db.TUDBs[headID] == nil {
		// lets try for inexistent but potential headers
		headID = getStringEncode(nonExistingHeaderName(headPath))
		if db.TUDBs[headID] == nil {
			return []string{}, nil
		}
		realHeader = false
	}

	htudb, err := db.GetSymbolsTUDB(headID)
	if err != nil {
		return nil, err
	}

	if realHeader {
		// if the header is a real not a potential one in the DB, check
		// if it exits and its mtime
		info, err := os.Stat(headPath)
		if err != nil {
			return db.getListOfFilenames(htudb.Includers), nil
		}
		hmtime = info.ModTime()
	}

	files := []string{}
	for includer := range htudb.Includers {
		tudb, err := db.GetSymbolsTUDB(includer)
		if err != nil {
			return nil, err
		}

		if hmtime.IsZero() || hmtime.After(tudb.Headers[headID]) {
			files = append(files, tudb.File)
		}
	}

	return files, nil
}

func (db *symbolsDB) UptodateFile(file string) (bool, bool, error) {
	info, err := os.Stat(file)
	if err != nil {
		return false, false, err
	}

	fileSha1 := getStringEncode(file)
	cache := db.TUDBs[fileSha1]
	if cache == nil {
		return false, false, nil
	}

	if cache.Mtime.Before(info.ModTime()) {
		return true, false, nil
	}

	return true, true, nil
}

func (db *symbolsDB) GetSymbolsTUDB(fid fileID) (*symbolsTUDB, error) {
	cache := db.TUDBs[fid]

	if cache == nil {
		return nil, fmt.Errorf("File not in DB")
	}

	cache.accTime = time.Now()

	if cache.tudb != nil {
		return cache.tudb, nil
	}

	var err error
	cache.tudb, err = db.LoadSymbolsTUDBFromSha1(fid)
	if err != nil {
		return nil, err
	}

	return cache.tudb, nil
}

func (db *symbolsDB) removeFileFromHeader(headerID, fid fileID) error {
	tudb, err := db.GetSymbolsTUDB(headerID)
	if err != nil {
		return err
	}

	delete(tudb.Includers, fid)
	db.TUDBs[headerID].dirty = true

	if len(tudb.Includers) == 0 {
		delete(db.TUDBs, headerID)
		os.Remove(getDBFileNameFromSha1(headerID))
	}

	return nil
}

func (db *symbolsDB) RemoveFileReferences(file string) error {
	fileSha1 := getStringEncode(file)

	tudb, err := db.GetSymbolsTUDB(fileSha1)
	if err != nil {
		return err
	}

	for h := range tudb.Headers {
		err := db.removeFileFromHeader(h, fileSha1)
		if err != nil {
			return err
		}
	}

	delete(db.TUDBs, fileSha1)
	os.Remove(getDBFileName(file))

	return nil
}

func (db *symbolsDB) GetSetFilesInDB() map[string]bool {
	fileSet := map[string]bool{}

	for _, cache := range db.TUDBs {
		if cache.Mtime.IsZero() {
			// ignore false files
			continue
		}
		fileSet[cache.Path] = true
	}

	return fileSet
}

func (db *symbolsDB) RemoveFileDepsReferences(file string) ([]string, error) {
	fileSha1 := getStringEncode(file)
	tudb, err := db.GetSymbolsTUDB(fileSha1)
	if err != nil {
		return nil, err
	}

	deps := db.getListOfFilenames(tudb.Includers)

	for _, dep := range deps {
		db.RemoveFileReferences(dep)
	}

	return deps, nil
}

func (db *symbolsDB) InsertTUDB(tudb *symbolsTUDB) error {
	var err error
	fileSha1 := getStringEncode(tudb.File)
	otudb := db.TUDBs[fileSha1]

	if otudb != nil {
		if otudb.Mtime.After(tudb.Mtime) {
			log.Panic("Inserting older tudb", otudb.Path, otudb.Mtime, tudb.Mtime)
		}

		db.RemoveFileReferences(tudb.File)
	}

	for header := range tudb.headersTUDB {
		var htudb *symbolsTUDB
		headerSha1 := getStringEncode(header)

		hcache := db.TUDBs[headerSha1]
		if hcache == nil {
			htudb = newSymbolsTUDB(header, tudb.Headers[headerSha1])
			hcache = &tuSymbolsDBCache{
				tudb:    htudb,
				Mtime:   htudb.Mtime,
				Path:    htudb.File,
				accTime: time.Now(),
			}
			db.TUDBs[headerSha1] = hcache
		} else {
			htudb, err = db.GetSymbolsTUDB(headerSha1)
			if err != nil {
				return err
			}
		}

		htudb.Includers[fileSha1] = true
		hcache.dirty = true
	}

	err = os.Rename(tudb.tmpFile, getDBFileName(tudb.File))
	if err != nil {
		return err
	}
	db.TUDBs[fileSha1] = &tuSymbolsDBCache{
		Mtime: tudb.Mtime,
		Path:  tudb.File,
	}

	return nil
}

///// symbolsDB query methods

func getSymbolLoc(sym *SymbolLocReq) *symbolLoc {
	fileSha1 := getStringEncode(filepath.Clean(sym.File))
	return &symbolLoc{
		fileSha1,
		int16(sym.Line),
		int16(sym.Col),
	}
}

func (db *symbolsDB) getSymbolLocReq(syms []symbolLoc) []*SymbolLocReq {
	res := []*SymbolLocReq{}

	for _, sym := range syms {
		cache := db.TUDBs[sym.File]
		if cache == nil {
			continue
		}

		res = append(res, &SymbolLocReq{
			cache.Path,
			int(sym.Line),
			int(sym.Col),
		})
	}

	if len(res) == 0 {
		return nil
	}

	return res
}

func getIncluder(htudb *symbolsTUDB) *symbolsTUDB {
	for fileSha1 := range htudb.Includers {
		tudb, err := db.GetSymbolsTUDB(fileSha1)
		if err != nil {
			log.Panic("unable to find includer")
		}
		return tudb
	}

	return nil
}

func (db *symbolsDB) GetSymbolDecl(useReq *SymbolLocReq) ([]*SymbolLocReq, error) {
	loc := getSymbolLoc(useReq)
	tudb, err := db.GetSymbolsTUDB(loc.File)
	if err != nil {
		return nil, err
	}

	// if header file, we should use any of its tudb
	if len(tudb.Includers) > 0 {
		tudb = getIncluder(tudb)
	}

	// checking if we have the location in DB
	id, exist := tudb.SymLoc[*loc]
	if !exist {
		return nil, fmt.Errorf("Symbol use not found")
	}

	data := tudb.SymData[id]
	return db.getSymbolLocReq(data.Decls), nil
}

func (db *symbolsDB) GetSymbolUses(useReq *SymbolLocReq) ([]*SymbolLocReq, error) {
	loc := getSymbolLoc(useReq)
	tudb, err := db.GetSymbolsTUDB(loc.File)
	if err != nil {
		return nil, err
	}
	fileSha1 := loc.File

	// if header file, we should use any of its tudb
	if len(tudb.Includers) > 0 {
		tudb = getIncluder(tudb)
		fileSha1 = getStringEncode(tudb.File)
	}

	// checking if we have the location in DB
	id, exist := tudb.SymLoc[*loc]
	if !exist {
		return nil, fmt.Errorf("Symbol use not found")
	}

	data := tudb.SymData[id]

	// add uses in this TU
	uses := make(map[symbolLoc]bool)
	for _, use := range data.Uses {
		uses[use.Loc] = true
	}
	// look for uses in declarations in header files
	for _, decl := range data.Decls {
		if decl.File == fileSha1 {
			continue
		}

		htudb, err := db.GetSymbolsTUDB(decl.File)
		if err != nil {
			continue
		}

		for tuSha1 := range htudb.Includers {
			if tuSha1 == fileSha1 {
				continue
			}

			otudb, err := db.GetSymbolsTUDB(tuSha1)
			if err != nil {
				continue
			}

			odata := otudb.SymData[id]
			for _, use := range odata.Uses {
				uses[use.Loc] = true
			}
		}
	}

	useLocs := []symbolLoc{}
	for useLoc := range uses {
		useLocs = append(useLocs, useLoc)
	}

	return db.getSymbolLocReq(useLocs), nil
}

func (db *symbolsDB) GetSymbolDef(useReq *SymbolLocReq) (*SymbolLocReq, error) {
	loc := getSymbolLoc(useReq)
	tudb, err := db.GetSymbolsTUDB(loc.File)
	if err != nil {
		return nil, err
	}
	fileSha1 := loc.File

	// if header file, we should use any of its tudb
	if len(tudb.Includers) > 0 {
		tudb = getIncluder(tudb)
		fileSha1 = getStringEncode(tudb.File)
	}

	// checking if we have the location in DB
	id, exist := tudb.SymLoc[*loc]
	if !exist {
		return nil, fmt.Errorf("Symbol use not found")
	}

	data := tudb.SymData[id]

	if data.DefAvail {
		return db.getSymbolLocReq([]symbolLoc{data.Def})[0], nil
	}

	for _, decl := range data.Decls {
		if decl.File == fileSha1 {
			continue
		}

		htudb, err := db.GetSymbolsTUDB(decl.File)
		if err != nil {
			continue
		}

		for tuSha1 := range htudb.Includers {
			if tuSha1 == fileSha1 {
				continue
			}

			otudb, err := db.GetSymbolsTUDB(tuSha1)
			if err != nil {
				continue
			}

			odata := otudb.SymData[id]
			if odata.DefAvail {
				return db.getSymbolLocReq([]symbolLoc{odata.Def})[0], nil
			}
		}
	}

	return nil, fmt.Errorf("Definition not found")
}

func (db *symbolsDB) GetAllSymbolDefs(use *SymbolLocReq) ([]*SymbolLocReq, error) {
	// TODO: this worked nice in the old sqlite DB as we had all
	// definitions in a single table. Now, we would have to look on all
	// files to get the same result. We could look in the includers of the
	// headers included. Return nothing for now.
	return nil, fmt.Errorf("Definition not found")
}

func (db *symbolsDB) PrintAndCheckSymbolsTUDB(inputPath string) error {
	path := filepath.Clean(inputPath)
	tudb, err := db.GetSymbolsTUDB(getStringEncode(path))
	if err != nil {
		return err
	}

	tudb.printAndCheck()

	return nil
}

///// TU Symbol methods

func newSymbolsTUDB(file string, mtime time.Time) *symbolsTUDB {
	return &symbolsTUDB{
		File:  file,
		Mtime: mtime,

		SymLoc:    make(map[symbolLoc]symbolID),
		SymData:   make(map[symbolID]symbolData),
		Headers:   make(map[fileID]time.Time),
		Includers: make(map[fileID]bool),

		headersTUDB: make(map[string]bool),
	}
}

func loadSymbolsTUDB(dbPath string) (*symbolsTUDB, error) {
	var tudb symbolsTUDB

	dbFile, err := os.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer dbFile.Close()

	dec := gob.NewDecoder(dbFile)

	err = dec.Decode(&tudb)
	if err != nil {
		return nil, err
	}

	return &tudb, nil
}

func (db *symbolsTUDB) SaveSymbolsTUDB(path string) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	dbFile, err := os.OpenFile(path, flags, 0644)
	if err != nil {
		return err
	}
	defer dbFile.Close()

	enc := gob.NewEncoder(dbFile)

	err = enc.Encode(db)
	if err != nil {
		return err
	}

	return nil
}

func (db *symbolsTUDB) getSymbolData(id symbolID, name string) symbolData {
	data, exist := db.SymData[id]
	if !exist {
		data = symbolData{
			Name:  name,
			Uses:  []symbolUse{},
			Decls: []symbolLoc{},
		}
	}

	return data
}

func (db *symbolsTUDB) insertSymbolDeclWithDef(sym, def *symbolInfo) {
	id := getStringEncode(sym.usr)
	symLoc := getSymbolLoc(&sym.loc)

	data := db.getSymbolData(id, sym.name)
	data.Decls = append(data.Decls, *symLoc)
	if def != nil {
		data.DefAvail = true
		data.Def = *getSymbolLoc(&def.loc)
	}

	db.SymLoc[*symLoc] = id
	db.SymData[id] = data
}

func (db *symbolsTUDB) InsertSymbolDecl(sym *symbolInfo) {
	db.insertSymbolDeclWithDef(sym, nil)
}

func (db *symbolsTUDB) InsertSymbolDeclWithDef(sym, def *symbolInfo) {
	db.insertSymbolDeclWithDef(sym, def)
}

func (db *symbolsTUDB) InsertSymbolUse(sym, dec *symbolInfo, funcCall bool) {
	if dec == nil {
		log.Println("use without decl, ignoring", sym)
		return
	}

	id := getStringEncode(dec.usr)
	symLoc := getSymbolLoc(&sym.loc)
	data := db.getSymbolData(id, sym.name)

	if _, exist := db.SymLoc[*symLoc]; exist {
		// The current symbol location was already registered. This
		// could be for two reasons:

		// 1. (TODO) A macro expanded in this location
		if db.SymLoc[*symLoc] != id {
			//symLoc = &db.SymData[db.SymLoc[*symLoc]].Decls[0]
			return
		}

		// 2. A call expression that is also a referenced symbol
		if len(data.Uses) > 0 {
			lastUse := &data.Uses[len(data.Uses)-1]
			if lastUse.Loc == *symLoc {
				lastUse.FuncCall = lastUse.FuncCall || funcCall
				return
			}
		}

	}

	data.Uses = append(data.Uses, symbolUse{
		Loc:      *symLoc,
		FuncCall: funcCall,
	})

	db.SymLoc[*symLoc] = id
	db.SymData[id] = data
}

func (db *symbolsTUDB) InsertHeader(inclPath string, headFile clang.File) {
	var headModTime time.Time
	var headPath string
	if headFile.Name() == "" {
		headModTime = time.Time{}
		headPath = nonExistingHeaderName(filepath.Clean(inclPath))
	} else {
		headModTime = headFile.Time()
		headPath = filepath.Clean(headFile.Name())
	}
	db.Headers[getStringEncode(headPath)] = headModTime
	db.headersTUDB[headPath] = true
}

func (db *symbolsTUDB) TempSaveDB() error {
	tmpFile, err := ioutil.TempFile(dbDirTmp, "")
	if err != nil {
		return err
	}
	defer tmpFile.Close()

	db.tmpFile = tmpFile.Name()
	err = db.SaveSymbolsTUDB(db.tmpFile)
	if err != nil {
		return err
	}

	return nil
}

func (db *symbolsTUDB) check() {
	// check sym data
	for _, data := range db.SymData {
		for _, use := range data.Uses {
			if _, exists := db.SymLoc[use.Loc]; !exists {
				log.Println("Use in SymData not in DB!", use)
			}
		}
	}

	// check uses
	for loc, id := range db.SymLoc {
		if _, exists := db.SymData[id]; !exists {
			log.Println("Use without data!", loc, id)
		}
	}
}

func (db *symbolsTUDB) printAndCheck() {
	if len(db.SymData) > 0 {
		fmt.Println("Data:")
		for id, data := range db.SymData {
			fmt.Println(id, "->")
			fmt.Println("\tName:", data.Name)
			fmt.Println("\tDefAvail:", data.DefAvail)
			fmt.Println("\tDef:", data.Def)
			fmt.Println("\tDecls:")
			for _, decl := range data.Decls {
				fmt.Println("\t\t", decl)
			}
			fmt.Println("\tUses:")
			for _, use := range data.Uses {
				fmt.Println("\t\t", use)
			}
		}
		fmt.Println("Loc:")
		for loc, id := range db.SymLoc {
			fmt.Println("\t", loc, "->", id)
		}
		db.check()
	}

	if len(db.Includers) > 0 {
		fmt.Println("Includers:")
		for fileID := range db.Includers {
			fmt.Println("\t", fileID)
		}
	}
}
