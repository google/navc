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

	"github.com/sbinet/go-clang"
)

/*
 * The symbols database consist of a key/value store per indexed C file. We
 * call this translation unit or TU (following clang's definition). Having a
 * database per translation unit will allow greater parallelism and higher
 * performance at indexing time. In the symbols directory, each database file
 * uses the sha1 of the original file name as file name. Each database will
 * have the following fields:
 *
 * - SymLoc (SymbolLoc -> SymbolID): Contains all the symbols uses in the
 * translatio unit. It maps symbol locations to symbol ID.
 *
 * - SymData (SymbolID -> SymbolData): Contains the data of all the symbols
 * indexed by symbol ID. Given any symbol location, we can find its symbol data
 * in the translation unit. The symbol data will have the list of declarations
 * of the symbol and the list of uses in the translation unit. If the
 * definition of the symbol is available in this translation unit, DefAvail
 * will be true and Def will hold the location of the definition.
 *
 * - Headers: Contains a list of all the header files included in the
 * translation unit.
 *
 * - Includers: In case the translation unit represent a header file, this list
 * will have all the translation units including this file. This is the only
 * information necessary for header files. Header files is where two
 * translation units meet. For instance, one function declared in a.h and used
 * by a.c can be defined in b.c. The meeting point of a.c and b.c is their
 * included header file a.h. Keeping this information is important to find all
 * the uses of a symbol or function definitions.
 *
 * - Misc: Contains the information of the file:
 *	* File: Name of the file.
 *	* Mtime: Last modification timestamp.
 *
 * Note 1: In the value part on the maps it is not necessary to store the
 * location as it is already in the key.
 * Note 2: SymbolsDB represents the whole index DB, while TUSymbolsDB
 * represents a single file DB.
 * Note 3: Header file databases does not have "SymLoc", "SymData", or
 * "Headers".
 */

type SymbolID [sha1.Size]byte
type FileID [sha1.Size]byte

type SymbolLoc struct {
	File FileID
	Line int16
	Col  int16
}

type SymbolUse struct {
	Loc      SymbolLoc
	FuncCall bool
}

type SymbolData struct {
	Uses     []SymbolUse
	Decls    []SymbolLoc
	DefAvail bool
	Def      SymbolLoc
}

type SymbolLocReq struct {
	File string
	Line int
	Col  int
}

type SymbolInfo struct {
	name string
	usr  string
	loc  SymbolLocReq
}

type TUSymbolsDB struct {
	File string

	// .c data
	Mtime   time.Time
	SymLoc  map[SymbolLoc]SymbolID
	SymData map[SymbolID]SymbolData
	Headers map[FileID]time.Time

	// .h lists
	Includers map[FileID]bool

	// used only while parsing
	headersTUDB map[string]bool
	tmpFile     string
}

type tuSymbolsDBCache struct {
	tudb  *TUSymbolsDB
	mtime time.Time
	path  string

	accTime time.Time
	dirty   bool
}

type SymbolsDB struct {
	tuDBs map[FileID]*tuSymbolsDBCache
}

// db directory path
var dbDirPath string

// db temp directory = dbDirPath + "/tmp"
var dbDirTmp string

///// Helper functions

func GetStringEncode(str string) [sha1.Size]byte {
	return sha1.Sum([]byte(str))
}

///// Symbols DB methods

func NewSymbolsDB(dbDirPathIn string) *SymbolsDB {
	// create index directory if it does not exist
	err := os.MkdirAll(dbDirPathIn+"/tmp", 0700)
	if err != nil {
		log.Panic("unable to create db dir ", err)
	}
	dbDirPath = dbDirPathIn
	dbDirTmp = dbDirPath + "/tmp"

	db := &SymbolsDB{make(map[FileID]*tuSymbolsDBCache)}

	// cache index
	filepath.Walk(dbDirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Println("error opening", path, "ignoring", err)
			return filepath.SkipDir
		}

		if path != dbDirPath && info.IsDir() {
			return filepath.SkipDir
		}

		tudb, ferr := LoadTUSymbolsDB(path)
		if ferr != nil {
			return nil
		}

		db.tuDBs[GetStringEncode(tudb.File)] = &tuSymbolsDBCache{
			tudb:  tudb,
			path:  tudb.File,
			mtime: tudb.Mtime,
		}

		return nil
	})

	return db
}

func (db *SymbolsDB) FlushDB(saveFrom time.Time) error {
	for _, cache := range db.tuDBs {
		if cache.tudb == nil {
			continue
		}

		if cache.accTime.After(saveFrom) {
			continue
		}

		if cache.dirty {
			err := cache.tudb.SaveTUSymbolsDB(getDBFileName(cache.path))
			if err != nil {
				return err
			}
		}
		cache.dirty = false
		cache.tudb = nil
	}

	return nil
}

func getDBFileNameFromSha1(fileID FileID) string {
	return dbDirPath + "/" + hex.EncodeToString(fileID[:])
}

func getDBFileName(file string) string {
	return getDBFileNameFromSha1(GetStringEncode(file))
}

func (db *SymbolsDB) LoadTUSymbolsDBFromSha1(file FileID) (*TUSymbolsDB, error) {
	tudb, err := LoadTUSymbolsDB(getDBFileNameFromSha1(file))
	if err != nil {
		return nil, err
	}

	return tudb, nil
}

func (db *SymbolsDB) getListOfFilenames(fileSet map[FileID]bool) []string {
	filenames := []string{}
	for fileID := range fileSet {
		filenames = append(filenames, db.tuDBs[fileID].path)
	}

	return filenames
}

func (db *SymbolsDB) GetOldIncluders(headPath string) ([]string, error) {
	headID := GetStringEncode(headPath)

	if db.tuDBs[headID] == nil {
		return []string{}, nil
	}

	htudb, err := db.GetTUSymbolsDB(headID)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(headPath)
	if err != nil {
		return db.getListOfFilenames(htudb.Includers), nil
	}

	files := []string{}
	hmtime := info.ModTime()
	for includer := range htudb.Includers {
		tudb, err := db.GetTUSymbolsDB(includer)
		if err != nil {
			return nil, err
		}

		if hmtime.After(tudb.Headers[headID]) {
			files = append(files, tudb.File)
		}
	}

	return files, nil
}

func (db *SymbolsDB) UptodateFile(file string) (bool, bool, error) {
	info, err := os.Stat(file)
	if err != nil {
		return false, false, err
	}

	fileSha1 := GetStringEncode(file)
	cache := db.tuDBs[fileSha1]
	if cache == nil {
		return false, false, nil
	}

	if cache.mtime.Before(info.ModTime()) {
		return true, false, nil
	}

	return true, true, nil
}

func (db *SymbolsDB) GetTUSymbolsDB(fileID FileID) (*TUSymbolsDB, error) {
	cache := db.tuDBs[fileID]

	if cache == nil {
		return nil, fmt.Errorf("File not in DB")
	}

	cache.accTime = time.Now()

	if cache.tudb != nil {
		return cache.tudb, nil
	}

	var err error
	cache.tudb, err = db.LoadTUSymbolsDBFromSha1(fileID)
	if err != nil {
		return nil, err
	}

	return cache.tudb, nil
}

func (db *SymbolsDB) removeFileFromHeader(headerID, fileID FileID) error {
	tudb, err := db.GetTUSymbolsDB(fileID)
	if err != nil {
		return err
	}

	delete(tudb.Includers, fileID)
	db.tuDBs[headerID].dirty = true

	if len(tudb.Includers) == 0 {
		delete(db.tuDBs, headerID)
		os.Remove(getDBFileNameFromSha1(headerID))
	}

	return nil
}

func (db *SymbolsDB) RemoveFileReferences(file string) error {
	fileSha1 := GetStringEncode(file)

	tudb, err := db.GetTUSymbolsDB(fileSha1)
	if err != nil {
		return err
	}

	for h := range tudb.Headers {
		err := db.removeFileFromHeader(h, fileSha1)
		if err != nil {
			return err
		}
	}

	delete(db.tuDBs, fileSha1)
	os.Remove(getDBFileName(file))

	return nil
}

func (db *SymbolsDB) GetSetFilesInDB() map[string]bool {
	fileSet := map[string]bool{}

	for _, cache := range db.tuDBs {
		fileSet[cache.path] = true
	}

	return fileSet
}

func (db *SymbolsDB) RemoveFileDepsReferences(file string) ([]string, error) {
	fileSha1 := GetStringEncode(file)
	tudb, err := db.GetTUSymbolsDB(fileSha1)
	if err != nil {
		return nil, err
	}

	deps := db.getListOfFilenames(tudb.Includers)

	for _, dep := range deps {
		db.RemoveFileReferences(dep)
	}

	return deps, nil
}

func (db *SymbolsDB) InsertTUDB(tudb *TUSymbolsDB) error {
	var err error
	fileSha1 := GetStringEncode(tudb.File)
	otudb := db.tuDBs[fileSha1]

	if otudb != nil {
		if otudb.mtime.After(tudb.Mtime) {
			os.Remove(tudb.tmpFile)
			return nil
		} else if otudb.mtime.Equal(tudb.Mtime) {
			odb, err := db.GetTUSymbolsDB(fileSha1)
			if err != nil {
				return err
			}
			for headSha1, headMTime := range tudb.Headers {
				if odb.Headers[headSha1].After(headMTime) {
					os.Remove(tudb.tmpFile)
					return nil
				}
			}
		}

		db.RemoveFileReferences(tudb.File)
	}

	for header := range tudb.headersTUDB {
		var htudb *TUSymbolsDB
		headerSha1 := GetStringEncode(header)

		hcache := db.tuDBs[headerSha1]
		if hcache == nil {
			htudb, err = NewTUSymbolsDB(header)
			if err != nil {
				return err
			}

			hcache = &tuSymbolsDBCache{
				tudb:    htudb,
				mtime:   htudb.Mtime,
				path:    htudb.File,
				accTime: time.Now(),
			}
			db.tuDBs[headerSha1] = hcache
		} else {
			htudb, err = db.GetTUSymbolsDB(headerSha1)
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
	db.tuDBs[fileSha1] = &tuSymbolsDBCache{
		mtime: tudb.Mtime,
		path:  tudb.File,
	}

	return nil
}

///// SymbolsDB query methods

func getSymbolLoc(sym *SymbolLocReq) *SymbolLoc {
	fileSha1 := GetStringEncode(filepath.Clean(sym.File))
	return &SymbolLoc{
		fileSha1,
		int16(sym.Line),
		int16(sym.Col),
	}
}

func (db *SymbolsDB) getSymbolLocReq(syms []SymbolLoc) []*SymbolLocReq {
	res := []*SymbolLocReq{}

	for _, sym := range syms {
		cache := db.tuDBs[sym.File]
		if cache == nil {
			continue
		}

		res = append(res, &SymbolLocReq{
			cache.path,
			int(sym.Line),
			int(sym.Col),
		})
	}

	if len(res) == 0 {
		return nil
	}

	return res
}

func getIncluder(htudb *TUSymbolsDB) *TUSymbolsDB {
	for fileSha1, _ := range htudb.Includers {
		tudb, err := db.GetTUSymbolsDB(fileSha1)
		if err != nil {
			log.Panic("unable to find includer")
		}
		return tudb
	}

	return nil
}

func (db *SymbolsDB) GetSymbolDecl(useReq *SymbolLocReq) []*SymbolLocReq {
	loc := getSymbolLoc(useReq)
	tudb, err := db.GetTUSymbolsDB(loc.File)
	if err != nil {
		return nil
	}

	// if header file, we should use any of its tudb
	if len(tudb.Includers) > 0 {
		tudb = getIncluder(tudb)
	}

	// checking if we have the location in DB
	id, exist := tudb.SymLoc[*loc]
	if !exist {
		return nil
	}

	data := tudb.SymData[id]
	return db.getSymbolLocReq(data.Decls)
}

func (db *SymbolsDB) GetSymbolUses(useReq *SymbolLocReq) []*SymbolLocReq {
	loc := getSymbolLoc(useReq)
	tudb, err := db.GetTUSymbolsDB(loc.File)
	if err != nil {
		return nil
	}
	fileSha1 := loc.File

	// if header file, we should use any of its tudb
	if len(tudb.Includers) > 0 {
		tudb = getIncluder(tudb)
		fileSha1 = GetStringEncode(tudb.File)
	}

	// checking if we have the location in DB
	id, exist := tudb.SymLoc[*loc]
	if !exist {
		return nil
	}

	data := tudb.SymData[id]

	// add uses in this TU
	uses := make(map[SymbolLoc]bool)
	for _, use := range data.Uses {
		uses[use.Loc] = true
	}
	// look for uses in declarations in header files
	for _, decl := range data.Decls {
		if decl.File == fileSha1 {
			continue
		}

		htudb, err := db.GetTUSymbolsDB(decl.File)
		if err != nil {
			continue
		}

		for tuSha1, _ := range htudb.Includers {
			if tuSha1 == fileSha1 {
				continue
			}

			otudb, err := db.GetTUSymbolsDB(tuSha1)
			if err != nil {
				continue
			}

			odata := otudb.SymData[id]
			for _, use := range odata.Uses {
				uses[use.Loc] = true
			}
		}
	}

	useLocs := []SymbolLoc{}
	for useLoc, _ := range uses {
		useLocs = append(useLocs, useLoc)
	}

	return db.getSymbolLocReq(useLocs)
}

func (db *SymbolsDB) GetSymbolDef(useReq *SymbolLocReq) *SymbolLocReq {
	loc := getSymbolLoc(useReq)
	tudb, err := db.GetTUSymbolsDB(loc.File)
	if err != nil {
		return nil
	}
	fileSha1 := loc.File

	// if header file, we should use any of its tudb
	if len(tudb.Includers) > 0 {
		tudb = getIncluder(tudb)
		fileSha1 = GetStringEncode(tudb.File)
	}

	// checking if we have the location in DB
	id, exist := tudb.SymLoc[*loc]
	if !exist {
		return nil
	}

	data := tudb.SymData[id]

	if data.DefAvail {
		return db.getSymbolLocReq([]SymbolLoc{data.Def})[0]
	}

	for _, decl := range data.Decls {
		if decl.File == fileSha1 {
			continue
		}

		htudb, err := db.GetTUSymbolsDB(decl.File)
		if err != nil {
			continue
		}

		for tuSha1, _ := range htudb.Includers {
			if tuSha1 == fileSha1 {
				continue
			}

			otudb, err := db.GetTUSymbolsDB(tuSha1)
			if err != nil {
				continue
			}

			odata := otudb.SymData[id]
			if odata.DefAvail {
				return db.getSymbolLocReq([]SymbolLoc{odata.Def})[0]
			}
		}
	}

	return nil
}

func (db *SymbolsDB) GetAllSymbolDefs(use *SymbolLocReq) []*SymbolLocReq {
	// TODO: this worked nice in the old sqlite DB as we had all
	// definitions in a single table. Now, we would have to look on all
	// files to get the same result. We could look in the includers of the
	// headers included. Return nothing for now.
	return nil
}

///// TU Symbol methods

func NewTUSymbolsDB(file string) (*TUSymbolsDB, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}

	return &TUSymbolsDB{
		File:  file,
		Mtime: info.ModTime(),

		SymLoc:    make(map[SymbolLoc]SymbolID),
		SymData:   make(map[SymbolID]SymbolData),
		Headers:   make(map[FileID]time.Time),
		Includers: make(map[FileID]bool),

		headersTUDB: make(map[string]bool),
	}, nil
}

func LoadTUSymbolsDB(dbPath string) (*TUSymbolsDB, error) {
	var tudb TUSymbolsDB

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

func (db *TUSymbolsDB) SaveTUSymbolsDB(path string) error {
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

func (db *TUSymbolsDB) getSymbolData(id SymbolID) SymbolData {
	data, exist := db.SymData[id]
	if !exist {
		data = SymbolData{
			Uses:  []SymbolUse{},
			Decls: []SymbolLoc{},
		}
	}

	return data
}

func (db *TUSymbolsDB) insertSymbolDeclWithDef(sym, def *SymbolInfo) {
	id := GetStringEncode(sym.usr)
	symLoc := getSymbolLoc(&sym.loc)

	data := db.getSymbolData(id)
	data.Decls = append(data.Decls, *symLoc)
	if def != nil {
		data.DefAvail = true
		data.Def = *getSymbolLoc(&def.loc)
	}

	db.SymLoc[*symLoc] = id
	db.SymData[id] = data
}

func (db *TUSymbolsDB) InsertSymbolDecl(sym *SymbolInfo) {
	db.insertSymbolDeclWithDef(sym, nil)
}

func (db *TUSymbolsDB) InsertSymbolDeclWithDef(sym, def *SymbolInfo) {
	db.insertSymbolDeclWithDef(sym, def)
}

func (db *TUSymbolsDB) InsertSymbolUse(sym, dec *SymbolInfo, funcCall bool) {
	if dec == nil {
		log.Println("use without decl, ignoring", sym)
		return
	}

	id := GetStringEncode(dec.usr)
	symLoc := getSymbolLoc(&sym.loc)

	data := db.getSymbolData(id)
	data.Uses = append(data.Uses, SymbolUse{
		Loc:      *symLoc,
		FuncCall: funcCall,
	})

	db.SymLoc[*symLoc] = id
	db.SymData[id] = data
}

func (db *TUSymbolsDB) InsertHeader(inclPath string, headFile clang.File) {
	var headModTime time.Time
	var headPath string
	if headFile.Name() == "" {
		headModTime = time.Time{}
		headPath = inclPath
	} else {
		headModTime = headFile.ModTime()
		headPath = headFile.Name()
	}
	db.Headers[GetStringEncode(headPath)] = headModTime
	db.headersTUDB[headPath] = true
}

func (db *TUSymbolsDB) TempSaveDB() error {
	tmpFile, err := ioutil.TempFile(dbDirTmp, "")
	if err != nil {
		return err
	}
	defer tmpFile.Close()

	db.tmpFile = tmpFile.Name()
	err = db.SaveTUSymbolsDB(db.tmpFile)
	if err != nil {
		return err
	}

	return nil
}
