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
	//"encoding/hex"
	//"io/ioutil"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/dgraph-io/badger"
	"github.com/go-clang/v3.6/clang"
)

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

type symbolsDB struct {
	// Key/Value Store DB handle
	backing *badger.DB
}

///// Symbols DB exported methods

func NewSymbolsDB(dbDirPath string) (*symbolsDB, error) {
	// create index directory if it does not exist
	err := os.Mkdir(dbDirPath, 0700)
	if err != nil {
		return nil, err
	}

	// Create the backing store handle
	options := badger.DefaultOptions
	options.Dir = dbDirPath
	options.ValueDir = dbDirPath
	options.SyncWrites = false
	backing, err := badger.Open(options)
	if err != nil {
		return nil, err
	}

	// Create the new object
	newDB := &symbolsDB{backing}

	return newDB, nil
}

func (db *symbolsDB) Close() error {
	return db.backing.Close()
}

func (db *symbolsDB) GetIncluders(headPath string) ([]string, error) {
	headID := getStringEncode(headPath)
	files := []string{}

	err := db.retryView(func(txn *badger.Txn) error {
		item, err := txn.Get(headID[:])
		if err != nil {
			return err
		}

		var htudb *symbolsTUDB
		err = item.Value(func(bin []byte) error {
			htudb, err = binToSymbolsTUDB(bin)
			return err
		})
		if err != nil {
			return err
		}

		files, err = db.getListOfFilenames(txn, htudb.Includers)
		return err
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}

func (db *symbolsDB) UptodateFile(file string) (bool, bool, error) {
	fileSha1 := getStringEncode(file)

	info, err := os.Stat(file)
	if err != nil {
		return false, false, err
	}

	exist := false
	uptodate := false
	err = db.retryView(func(txn *badger.Txn) error {
		item, err := txn.Get(fileSha1[:])
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		exist = true

		var tudb *symbolsTUDB
		err = item.Value(func(bin []byte) error {
			tudb, err = binToSymbolsTUDB(bin)
			return err
		})
		if err != nil {
			return err
		}

		uptodate = !tudb.Mtime.Before(info.ModTime())

		return nil
	})

	return exist, uptodate, err
}

func (db *symbolsDB) RemoveFileReferences(file string) error {
	fileSha1 := getStringEncode(file)

	err := db.retryUpdate(func(txn *badger.Txn) error {
		item, err := txn.Get(fileSha1[:])
		if err != nil {
			return err
		}

		var tudb *symbolsTUDB
		err = item.Value(func(bin []byte) error {
			tudb, err = binToSymbolsTUDB(bin)
			return err
		})
		if err != nil {
			return err
		}

		for h := range tudb.Headers {
			htudb, err := getSymbolsTUDB(txn, h)
			if err != nil {
				return err
			}

			delete(htudb.Includers, fileSha1)

			if len(htudb.Includers) == 0 {
				txn.Delete(h[:])
				continue
			}

			bin, err := symbolsTUDBToBin(htudb)
			if err != nil {
				return err
			}

			err = txn.Set(h[:], bin)
			if err != nil {
				return err
			}
		}

		err = txn.Delete(fileSha1[:])
		return err
	})

	return err
}

func (db *symbolsDB) GetSetFilesInDB() (map[string]bool, error) {
	fileSet := map[string]bool{}

	err := db.retryView(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			var tudb *symbolsTUDB
			err := it.Item().Value(func(bin []byte) error {
				var err error
				tudb, err = binToSymbolsTUDB(bin)
				return err
			})
			if err != nil {
				return err
			}

			if tudb.Mtime.IsZero() {
				// ignore false files
				continue
			}

			fileSet[tudb.File] = true
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return fileSet, nil
}

func (db *symbolsDB) InsertTUDB(tudb *symbolsTUDB) error {
	fileSha1 := getStringEncode(tudb.File)

	err := db.retryUpdate(func(txn *badger.Txn) error {
		for h := range tudb.headersTUDB {
			headerSha1 := getStringEncode(h)

			htudb, err := getSymbolsTUDB(txn, headerSha1)
			if err == badger.ErrKeyNotFound {
				htudb = NewSymbolsTUDB(h, tudb.Headers[headerSha1])
			} else if err != nil {
				return err
			}

			htudb.Includers[fileSha1] = true

			bin, err := symbolsTUDBToBin(htudb)
			if err != nil {
				return err
			}

			err = txn.Set(headerSha1[:], bin)
			if err != nil {
				return err
			}
		}

		bin, err := symbolsTUDBToBin(tudb)
		if err != nil {
			return err
		}

		err = txn.Set(fileSha1[:], bin)
		return err
	})

	return err
}

///// Symbols DB helper methods

func (db *symbolsDB) getListOfFilenames(txn *badger.Txn, fileSet map[fileID]bool) ([]string, error) {
	filenames := []string{}
	for fid := range fileSet {
		tudb, err := getSymbolsTUDB(txn, fid)
		if err != nil {
			return nil, err
		}

		filenames = append(filenames, tudb.File)
	}

	return filenames, nil
}

func getSymbolsTUDB(txn *badger.Txn, fid fileID) (*symbolsTUDB, error) {
	item, err := txn.Get(fid[:])
	if err != nil {
		return nil, err
	}

	var tudb *symbolsTUDB
	err = item.Value(func(bin []byte) error {
		tudb, err = binToSymbolsTUDB(bin)
		return err
	})
	if err != nil {
		return nil, err
	}

	return tudb, nil
}

func binToSymbolsTUDB(bin []byte) (*symbolsTUDB, error) {
	var tudb symbolsTUDB

	buf := bytes.NewBuffer(bin)
	dec := gob.NewDecoder(buf)
	err := dec.Decode(&tudb)
	if err != nil {
		return nil, err
	}

	return &tudb, nil
}

func symbolsTUDBToBin(tudb *symbolsTUDB) ([]byte, error) {
	var buf bytes.Buffer

	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&tudb)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func getStringEncode(str string) [sha1.Size]byte {
	return sha1.Sum([]byte(str))
}

func (db *symbolsDB) retryView(fn func(txn *badger.Txn) error) error {
	var err error

	for {
		err = db.backing.View(fn)
		if err != badger.ErrConflict {
			break
		}
	}

	return err
}

func (db *symbolsDB) retryUpdate(fn func(txn *badger.Txn) error) error {
	var err error

	for {
		err = db.backing.Update(fn)
		if err != badger.ErrConflict {
			break
		}
	}

	return err
}

/*

///// TU Symbol methods

func NewSymbolsTUDB(file string, mtime time.Time) *symbolsTUDB {
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
		headPath = filepath.Clean(inclPath)
	} else {
		headModTime = headFile.Time()
		headPath = filepath.Clean(headFile.Name())
	}
	db.Headers[getStringEncode(headPath)] = headModTime
	db.headersTUDB[headPath] = true
}

///// SymbolsTUDB helper methods

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

func getSymbolLoc(sym *SymbolLocReq) *symbolLoc {
	fileSha1 := getStringEncode(filepath.Clean(sym.File))
	return &symbolLoc{
		fileSha1,
		int16(sym.Line),
		int16(sym.Col),
	}
}

///// Symbols DB query methods

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
*/
