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
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
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

type TUSymbolsDB struct {
	File   string
	DBPath string
	Info   []byte

	// .c maps
	Defs    map[SymbolLoc]SymbolDef
	Decls   map[SymbolLoc]SymbolDecl
	Uses    map[SymbolLoc]SymbolUse
	Headers [][sha1.Size]byte

	// .h lists
	Includers map[[sha1.Size]byte]bool
}

type SymbolsDB struct {
	dbDirPath string
}

///// Helper functions

func getBytes(data interface{}) []byte {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, data)
	if err != nil {
		log.Panic("unable to convert to []byte ", err)
	}

	return buf.Bytes()
}

func getFileInfoBytes(file string) ([]byte, error) {
	fi, err := os.Stat(file)
	if err != nil {
		return nil, err
	}

	timeBytes, err := fi.ModTime().MarshalBinary()
	if err != nil {
		return nil, err
	}

	data := []interface{}{
		fi.Size(),
		fi.Mode(),
		timeBytes,
	}
	buf := new(bytes.Buffer)
	for _, v := range data {
		err := binary.Write(buf, binary.BigEndian, v)
		if err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

func getFileNameSha1(file string) [sha1.Size]byte {
	return sha1.Sum([]byte(file))
}

///// Symbols DB methods

func NewSymbolsDB(dbDirPath string) *SymbolsDB {
	if _, err := os.Stat(dbDirPath); os.IsNotExist(err) {
		err := os.Mkdir(dbDirPath, 0700)
		if err != nil {
			log.Panic("unable to create db dir ", err)
		}
	}
	return &SymbolsDB{dbDirPath}
}

func (fac *SymbolsDB) getDBFileNameFromSha1(fileSha1 [sha1.Size]byte) string {
	return fac.dbDirPath + "/" + hex.EncodeToString(fileSha1[:])
}

func (fac *SymbolsDB) getDBFileName(file string) string {
	return fac.getDBFileNameFromSha1(getFileNameSha1(file))
}

func (fac *SymbolsDB) NewTUSymbolsDB(file string) (*TUSymbolsDB, error) {
	info, err := getFileInfoBytes(file)
	if err != nil {
		return nil, err
	}

	return &TUSymbolsDB{
		File:   file,
		Info:   info,
		DBPath: fac.getDBFileName(file),

		Defs:      make(map[SymbolLoc]SymbolDef),
		Decls:     make(map[SymbolLoc]SymbolDecl),
		Uses:      make(map[SymbolLoc]SymbolUse),
		Headers:   [][sha1.Size]byte{},
		Includers: make(map[[sha1.Size]byte]bool),
	}, nil
}

func (fac *SymbolsDB) LoadTUSymbolsDBFromSha1(file [sha1.Size]byte) (*TUSymbolsDB, error) {
	var tudb TUSymbolsDB

	dbFile, err := os.Open(fac.getDBFileNameFromSha1(file))
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

func (fac *SymbolsDB) LoadTUSymbolsDB(file string) (*TUSymbolsDB, error) {
	tudb, err := fac.LoadTUSymbolsDBFromSha1(getFileNameSha1(file))
	if os.IsNotExist(err) {
		return fac.NewTUSymbolsDB(file)
	}

	return tudb, err
}

func (db *SymbolsDB) SaveTUSymbolsDB(tudb *TUSymbolsDB) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	dbFile, err := os.OpenFile(db.getDBFileName(tudb.File), flags, 0644)
	if err != nil {
		return err
	}
	defer dbFile.Close()

	enc := gob.NewEncoder(dbFile)

	err = enc.Encode(tudb)
	if err != nil {
		return err
	}

	return nil
}

func (fac *SymbolsDB) UptodateFile(file string) (bool, bool, error) {
	info, err := getFileInfoBytes(file)
	if err != nil {
		return false, false, err
	}

	if _, err := os.Stat(fac.getDBFileName(file)); os.IsNotExist(err) {
		return false, false, nil
	}

	db, err := fac.LoadTUSymbolsDB(file)
	if err != nil {
		return false, false, err
	}

	var uptodate bool
	if bytes.Equal(info, db.Info) {
		uptodate = true
	}

	return true, uptodate, nil
}

func (fac *SymbolsDB) removeFileFromHeader(headerSha1, fileSha1 [sha1.Size]byte) error {
	db, err := fac.LoadTUSymbolsDBFromSha1(headerSha1)
	if err != nil {
		return err
	}

	delete(db.Includers, fileSha1)

	if len(db.Headers) == 0 {
		err := os.Remove(fac.getDBFileNameFromSha1(headerSha1))
		if err != nil {
			return err
		}
	} else {
		err := fac.SaveTUSymbolsDB(db)
		if err != nil {
			return err
		}
	}

	return nil
}

func (fac *SymbolsDB) RemoveFileReferences(file string) error {
	db, err := fac.LoadTUSymbolsDB(file)
	if err != nil {
		return err
	}

	for _, h := range db.Headers {
		err := fac.removeFileFromHeader(h, getFileNameSha1(file))
		if err != nil {
			return err
		}
	}

	err = os.Remove(fac.getDBFileName(file))
	if err != nil {
		return err
	}

	return nil
}

func (db *SymbolsDB) InsertDependency(fileDB *TUSymbolsDB, head string) {
	fileSha1 := getFileNameSha1(fileDB.File)
	headSha1 := getFileNameSha1(head)

	fileDB.Headers = append(fileDB.Headers, headSha1)

	headDB, err := db.LoadTUSymbolsDB(head)
	if err != nil {
		log.Panic("unable to load DB for", head, err)
	}

	headDB.Includers[fileSha1] = true

	err = db.SaveTUSymbolsDB(headDB)
	if err != nil {
		log.Panic("unable to write DB for", head, err)
	}
}

func (db *SymbolsDB) GetSetFilesInDB() map[string]bool {
	fileSet := map[string]bool{}

	err := filepath.Walk(db.dbDirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			tudb, err := db.LoadTUSymbolsDB(path)
			if err != nil {
				return err
			}

			fileSet[tudb.File] = true
		}

		return nil
	})
	if err != nil {
		log.Panic("unable to read database ", err)
	}

	return fileSet
}

func (db *SymbolsDB) RemoveFileDepsReferences(file string) []string {
	// TODO: remove dependent files and return list of files removed
	return nil
}

///// SymbolsDB query methods
// TODO: implement

func (db *SymbolsDB) GetSymbolDecl(use *SymbolLoc) *SymbolLoc {
	return nil
}

func (db *SymbolsDB) GetSymbolUses(use *SymbolLoc) []*SymbolLoc {
	return nil
}

func (db *SymbolsDB) GetSymbolDef(use *SymbolLoc) *SymbolLoc {
	return nil
}

func (db *SymbolsDB) GetAllSymbolDefs(use *SymbolLoc) []*SymbolLoc {
	// TODO: this worked nice in the old sqlite DB as we had all
	// definitions in a single table. Now, we would have to look on all
	// files to get the same result. We could look in the includers of the
	// headers included. Return nothing for now.
	return nil
}

///// TU Symbol insertion methods

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

///// Other TU symbolsDB methods

func (db *TUSymbolsDB) Encode(str string) [sha1.Size]byte {
	return sha1.Sum([]byte(str))
}
