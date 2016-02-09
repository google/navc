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
)

/*
 * The symbols database consist of a key/value store per indexed C file. We
 * call this translation unit or TU (following clang's definition). Having a
 * database per translation unit will allow greater parallelism and higher
 * performance at indexing time. In the symbols directory, each database file
 * is stored with the sha1 of the file name. Each database will have the
 * following buckets:
 *
 * - defs (SymbolLoc -> SymbolDef): Contains all the definitions in the
 * translation unit. Each SymbolDef will have the id of the symbol defined
 * (function or structure).
 *
 * - decls (SymbolLoc -> SymbolDecl): Contains all the declaration in the
 * translation unit. Each SymbolDecl will have the id, and the list of symbol
 * uses of this declaration. Also, if the definition is available, SymbolDecl
 * will point to it in the defs bucket.
 *
 * - uses (SymbolLoc -> SymbolUse): Contains all the uses in the translation
 * unit. Each SymbolUse contains the id of the symbol and a pointer to the
 * declaration in the declaration bucket. Note that the declaration may not be
 * available in codes that does not compile.
 *
 * - file: Contains the information of the file:
 *	* name: Name of the file
 *	* info: Information in fstat
 *
 * - headers: Contains a list of all the header files included in the
 * translation unit.
 *
 * Note 1: In the value part on the buckets it is not necessary to store the
 * location as it is already in the key.
 * Note 2: SymbolsDB represents the whole index DB, while TUSymbolsDB
 * represents a single file DB.
 *
 * Header files is where two translation units meet. For instance, one function
 * declared in a.h and used by a.c can be defined in b.c. The meeting point of
 * a.c and b.c is their included header file a.h. Keeping this information is
 * important to find all the uses of a symbol or function definitions. Header
 * file database will have the files that include it in the bucket called
 * "includers."
 *
 * Note 3: Header file databases does not have "defs", "decls", or "uses"
 * buckets.
 *
 * Note 4: Currently, to remove all references of a file, we only wipe the
 * content but do not remove the file itself. This is because we don't know if
 * another transaction is in progress. We will remove this files in the next
 * daemon initialization.
 */

type SymbolLoc struct {
	File [sha1.Size]byte
	Line int16
	Col  int16
}

type SymbolID struct {
	Name  [sha1.Size]byte
	Unisr [sha1.Size]byte
}

type SymbolUse struct {
	Id       SymbolID
	Decl     SymbolLoc
	FuncCall bool
}

type SymbolDecl struct {
	Id       SymbolID
	DefAvail bool
	Def      SymbolLoc
}

type SymbolDef struct {
	Id SymbolID
}

type SymbolInfo struct {
	id  SymbolID
	loc SymbolLoc
}

type SymbolLocReq struct {
	File string
	Line int
	Col  int
}

type TUSymbolsDB struct {
	File  string
	Mtime time.Time

	// .c maps
	Defs    map[SymbolLoc]SymbolDef
	Decls   map[SymbolLoc]SymbolDecl
	Uses    map[SymbolLoc]SymbolUse
	Headers [][sha1.Size]byte

	// used only while parsing
	headersTUDB []string
	tmpFile     string

	// .h lists
	Includers map[[sha1.Size]byte]bool
}

type tuSymbolsDBCache struct {
	tudb  *TUSymbolsDB
	mtime time.Time
	path  string

	accTime time.Time
}

type SymbolsDB struct {
	tuDBs map[[sha1.Size]byte]*tuSymbolsDBCache
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

	db := &SymbolsDB{make(map[[sha1.Size]byte]*tuSymbolsDBCache)}

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

		err := cache.tudb.SaveTUSymbolsDB(getDBFileName(cache.path))
		if err != nil {
			return err
		}
		cache.tudb = nil
	}

	return nil
}

func getDBFileNameFromSha1(fileSha1 [sha1.Size]byte) string {
	return dbDirPath + "/" + hex.EncodeToString(fileSha1[:])
}

func getDBFileName(file string) string {
	return getDBFileNameFromSha1(GetStringEncode(file))
}

func (db *SymbolsDB) LoadTUSymbolsDBFromSha1(file [sha1.Size]byte) (*TUSymbolsDB, error) {
	tudb, err := LoadTUSymbolsDB(getDBFileNameFromSha1(file))
	if err != nil {
		return nil, err
	}

	return tudb, nil
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

func (db *SymbolsDB) GetTUSymbolsDB(fileSha1 [sha1.Size]byte) (*TUSymbolsDB, error) {
	cache := db.tuDBs[fileSha1]

	if cache == nil {
		return nil, fmt.Errorf("File not in DB")
	}

	if cache.tudb != nil {
		return cache.tudb, nil
	}

	var err error
	cache.tudb, err = db.LoadTUSymbolsDBFromSha1(fileSha1)
	if err != nil {
		return nil, err
	}

	return cache.tudb, nil
}

func (db *SymbolsDB) removeFileFromHeader(headerSha1, fileSha1 [sha1.Size]byte) error {
	tudb, err := db.GetTUSymbolsDB(headerSha1)
	if err != nil {
		return err
	}

	delete(tudb.Includers, fileSha1)
	db.tuDBs[headerSha1].accTime = time.Now()

	if len(tudb.Includers) == 0 {
		delete(db.tuDBs, headerSha1)
		os.Remove(getDBFileNameFromSha1(headerSha1))
	}

	return nil
}

func (db *SymbolsDB) RemoveFileReferences(file string) error {
	fileSha1 := GetStringEncode(file)

	tudb, err := db.GetTUSymbolsDB(fileSha1)
	if err != nil {
		return err
	}

	for _, h := range tudb.Headers {
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

	deps := []string{}
	for depSha1, _ := range tudb.Includers {
		depTudb := db.tuDBs[depSha1]
		deps = append(deps, depTudb.path)
	}

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
		}

		db.RemoveFileReferences(tudb.File)
	}

	for _, header := range tudb.headersTUDB {
		var htudb *TUSymbolsDB
		headerSha1 := GetStringEncode(header)

		hcache := db.tuDBs[headerSha1]
		if hcache == nil {
			htudb, err = NewTUSymbolsDB(header)
			if err != nil {
				return err
			}

			hcache = &tuSymbolsDBCache{
				tudb:  htudb,
				mtime: htudb.Mtime,
				path:  htudb.File,
			}
			db.tuDBs[headerSha1] = hcache
		} else {
			htudb, err = db.GetTUSymbolsDB(headerSha1)
			if err != nil {
				return err
			}
		}

		htudb.Includers[fileSha1] = true
		hcache.accTime = time.Now()
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
// TODO: in all these query implementation we are assuming that the request is
// coming from a .c file. When coming from headers, the handling is different.
// TODO: when more than one declaration is available, a symbol use points to
// its closes declaration. This can be a problem when returning the
// declaration. For instance, if we look for the declaration of a definition,
// it will point to the previous declaration. But if we look for the
// declaration of a use, it will point to the definition. We need to define
// what to do in these cases. Return multiple declarations?

func getSymbolLoc(sym *SymbolLocReq) *SymbolLoc {
	fileSha1 := GetStringEncode(filepath.Clean(sym.File))
	return &SymbolLoc{
		fileSha1,
		int16(sym.Line),
		int16(sym.Col),
	}
}

func (db *SymbolsDB) getSymbolLocReq(sym SymbolLoc) *SymbolLocReq {
	cache := db.tuDBs[sym.File]
	if cache == nil {
		return nil
	}

	return &SymbolLocReq{
		cache.path,
		int(sym.Line),
		int(sym.Col),
	}
}

func (db *SymbolsDB) GetSymbolDecl(useReq *SymbolLocReq) *SymbolLocReq {
	loc := getSymbolLoc(useReq)
	tudb, err := db.GetTUSymbolsDB(loc.File)
	if err != nil {
		return nil
	}

	// checking if we got a definition
	_, exist := tudb.Defs[*loc]
	if exist {
		log.Println(tudb.Decls)
		for dloc, decl := range tudb.Decls {
			if decl.DefAvail && decl.Def == *loc {
				return db.getSymbolLocReq(dloc)
			}
		}
	}

	// checking if we got a declaration
	_, exist = tudb.Decls[*loc]
	if exist {
		return useReq
	}

	// then, we got a regular use
	return db.getSymbolLocReq(tudb.Uses[*loc].Decl)
}

func (db *SymbolsDB) getSymbolUses(decl *SymbolLoc, tudb *TUSymbolsDB) []*SymbolLocReq {
	uses := []*SymbolLocReq{}
	for loc, use := range tudb.Uses {
		if *decl == use.Decl {
			uses = append(uses, db.getSymbolLocReq(loc))
		}
	}

	return uses
}

func (db *SymbolsDB) GetSymbolUses(useReq *SymbolLocReq) []*SymbolLocReq {
	loc := getSymbolLoc(useReq)
	tudb, err := db.GetTUSymbolsDB(loc.File)
	if err != nil {
		return nil
	}

	var declLoc SymbolLoc

	// checking if we got a definition
	_, exist := tudb.Defs[*loc]
	if exist {
		for dloc, decl := range tudb.Decls {
			if decl.DefAvail && decl.Def == *loc {
				declLoc = dloc
				break
			}
		}
	}

	// checking if we got a declaration
	_, exist = tudb.Decls[*loc]
	if exist {
		declLoc = *loc
	}

	// checking if we got a regular use
	symUse, exist := tudb.Uses[*loc]
	if exist {
		declLoc = symUse.Decl
	}

	// by now we have the declaration of the use

	// if the declaration is in the same file, the uses are local
	if declLoc.File == loc.File {
		return db.getSymbolUses(&declLoc, tudb)
	}

	// the uses are in all the TUs including the header file with declLoc
	var uses []*SymbolLocReq
	htudb, err := db.GetTUSymbolsDB(declLoc.File)
	if err != nil {
		return nil
	}
	for tuSha1, _ := range htudb.Includers {
		otudb, err := db.GetTUSymbolsDB(tuSha1)
		if err != nil {
			continue
		}

		uses = append(uses, db.getSymbolUses(&declLoc, otudb)...)
	}

	return uses
}

func (db *SymbolsDB) GetSymbolDef(useReq *SymbolLocReq) *SymbolLocReq {
	loc := getSymbolLoc(useReq)
	tudb, err := db.GetTUSymbolsDB(loc.File)
	if err != nil {
		return nil
	}

	// checking if we got a definition
	_, exist := tudb.Defs[*loc]
	if exist {
		return useReq
	}

	var declLoc SymbolLoc // declaration location

	// checking if we got a declaration
	decl, exist := tudb.Decls[*loc]
	if exist {
		if decl.DefAvail {
			return db.getSymbolLocReq(decl.Def)
		}
		declLoc = *loc
	}

	// if we got a regular use
	use, exist := tudb.Uses[*loc]
	if exist {
		decl = tudb.Decls[use.Decl]
		if decl.DefAvail {
			return db.getSymbolLocReq(decl.Def)
		}
		declLoc = use.Decl
	}

	// at this point I have a valid decl without def

	// if the declaration is in the same file, we should not look further
	if declLoc.File == loc.File {
		return nil
	}

	// the declaration is in a header, lets try to find the definition in
	// another TU
	htudb, err := db.GetTUSymbolsDB(declLoc.File)
	if err != nil {
		return nil
	}

	for tuSha1, _ := range htudb.Includers {
		otudb, err := db.GetTUSymbolsDB(tuSha1)
		if err != nil {
			continue
		}

		decl := otudb.Decls[declLoc]
		if decl.DefAvail {
			return db.getSymbolLocReq(decl.Def)
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

		Defs:      make(map[SymbolLoc]SymbolDef),
		Decls:     make(map[SymbolLoc]SymbolDecl),
		Uses:      make(map[SymbolLoc]SymbolUse),
		Headers:   [][sha1.Size]byte{},
		Includers: make(map[[sha1.Size]byte]bool),
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

func (db *TUSymbolsDB) insertSymbolDeclWithDef(sym, def *SymbolInfo) {
	decl := SymbolDecl{Id: sym.id}
	if def != nil {
		decl.DefAvail = true
		decl.Def = def.loc

		db.Defs[def.loc] = SymbolDef{def.id}
	}

	db.Decls[sym.loc] = decl
}

func (db *TUSymbolsDB) InsertSymbolDecl(sym *SymbolInfo) {
	db.insertSymbolDeclWithDef(sym, nil)
}

func (db *TUSymbolsDB) InsertSymbolDeclWithDef(sym, def *SymbolInfo) {
	db.insertSymbolDeclWithDef(sym, def)
}

func (db *TUSymbolsDB) InsertSymbolUse(sym, dec *SymbolInfo, funcCall bool) {
	use := SymbolUse{Id: sym.id, FuncCall: funcCall}
	if dec != nil {
		use.Decl = dec.loc
	}

	db.Uses[sym.loc] = use
}

func (db *TUSymbolsDB) InsertHeader(headFile string) {
	db.Headers = append(db.Headers, GetStringEncode(headFile))
	db.headersTUDB = append(db.headersTUDB, headFile)
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
