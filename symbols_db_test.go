/*
 * Copyright 2019 Google Inc. All Rights Reserved.
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
	"encoding/hex"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/dgraph-io/badger"
)

const testDir = "test_tmp/"

func openDB(t *testing.T) *symbolsDB {
	sdb, err := NewSymbolsDB(testDir)
	if err != nil {
		t.Errorf("Error opening db: %v", err)
	}

	return sdb
}

func closeDB(t *testing.T, sdb *symbolsDB) {
	if err := sdb.Close(); err != nil {
		t.Errorf("Error closing db: %v", err)
	}

	if err := os.RemoveAll(testDir); err != nil {
		t.Errorf("Error removing dir %s: %v", testDir, err)
	}
}

func getRandomFileName(t *testing.T) string {
	var sha1Value [sha1.Size]byte

	_, err := rand.Read(sha1Value[:])
	if err != nil {
		t.Errorf("Error reading random: %v", err)
	}

	return hex.EncodeToString(sha1Value[:])
}

func getRandSymbolInfo(t *testing.T, tudb *symbolsTUDB) *symbolInfo {
	return &symbolInfo{
		name: getRandomFileName(t),
		usr:  getRandomFileName(t),
		//loc:  getRandSymbolLocReq(tudb),
		loc: SymbolLocReq{
			File: tudb.File,
			Line: rand.Intn(10000),
			Col:  rand.Intn(80),
		},
	}
}

func populateRandSymbols(t *testing.T, tudb *symbolsTUDB) {
	for s := 0; s < 100; s++ {
		sym := getRandSymbolInfo(t, tudb)
		if rand.Intn(100) < 10 {
			tudb.InsertSymbolDecl(sym)
		} else {
			tudb.InsertSymbolDeclWithDef(sym, sym)
		}
	}
}

func getRandTUDBs(t *testing.T, hs int, cs int) ([]*symbolsTUDB, map[string][]string, map[string]bool) {
	var headers []string
	var tudbs []*symbolsTUDB

	filesSet := make(map[string]bool)
	headIncluders := make(map[string][]string)
	now := time.Now()

	for h := 0; h < hs; h++ {
		fileName := getRandomFileName(t) + ".h"
		filePath := testDir + fileName

		file, err := os.Create(filePath)
		if err != nil {
			t.Errorf("Couldn't open file %s: %v", filePath, err)
		}
		err = file.Close()
		if err != nil {
			t.Errorf("Couldn't close file %s: %v", filePath, err)
		}

		headers = append(headers, fileName)
		headIncluders[filePath] = []string{}
		filesSet[filePath] = true
	}

	for c := 0; c < cs; c++ {
		fileName := getRandomFileName(t) + ".c"
		filePath := testDir + fileName
		randHeader := headers[rand.Uint32()%uint32(len(headers))]
		randHeaderPath := testDir + randHeader

		file, err := os.Create(filePath)
		if err != nil {
			t.Errorf("Couldn't open file %s: %v", filePath, err)
		}

		_, err = file.WriteString("#include \"" + randHeader + "\"")
		if err != nil {
			t.Errorf("Couldn't write file %s: %v", filePath, err)
		}
		err = file.Close()
		if err != nil {
			t.Errorf("Couldn't close file %s: %v", filePath, err)
		}

		tudb := NewSymbolsTUDB(filePath, now)
		tudb.Headers[getStringEncode(randHeaderPath)] = now
		tudb.headersTUDB[randHeaderPath] = true

		populateRandSymbols(t, tudb)

		tudbs = append(tudbs, tudb)
		headIncluders[randHeaderPath] = append(headIncluders[randHeaderPath], filePath)
		filesSet[filePath] = true
	}

	return tudbs, headIncluders, filesSet
}

func insertTUDBs(t *testing.T, db *symbolsDB, tudbs []*symbolsTUDB) {
	for _, tudb := range tudbs {
		err := db.InsertTUDB(tudb)
		if err != nil {
			t.Errorf("Error inserting tudb %s: %v", tudb.File, err)
		}
	}
}

func removeTUDBs(t *testing.T, db *symbolsDB, tudbs []*symbolsTUDB) {
	for _, tudb := range tudbs {
		err := db.RemoveFileReferences(tudb.File)
		if err != nil {
			t.Errorf("Error removing reference %s: %v", tudb.File, err)
		}
	}
}

func TestOpenCloseBacking(t *testing.T) {
	sdb := openDB(t)
	defer closeDB(t, sdb)
}

func TestInsertTUDBs(t *testing.T) {
	sdb := openDB(t)
	defer closeDB(t, sdb)

	numHeaders := 10
	numCs := 100
	tudbs, _, _ := getRandTUDBs(t, numHeaders, numCs)
	insertTUDBs(t, sdb, tudbs)
}

func TestGetSetFilesInDB(t *testing.T) {
	sdb := openDB(t)
	defer closeDB(t, sdb)

	numHeaders := 10
	numCs := 100
	tudbs, _, filesSet := getRandTUDBs(t, numHeaders, numCs)
	insertTUDBs(t, sdb, tudbs)

	inDB, err := sdb.GetSetFilesInDB()
	if err != nil {
		t.Errorf("Unable to get the set of files: %v", err)
	}

	if !reflect.DeepEqual(filesSet, inDB) {
		t.Errorf("Files set in DB not correct: %v != %v", filesSet, inDB)
	}
}

func TestGetIncluders(t *testing.T) {
	sdb := openDB(t)
	defer closeDB(t, sdb)

	numHeaders := 10
	numCs := 100
	tudbs, headers, _ := getRandTUDBs(t, numHeaders, numCs)
	insertTUDBs(t, sdb, tudbs)

	for h, cs := range headers {
		incls, err := sdb.GetIncluders(h)
		if err != nil {
			t.Fatalf("Error getting includers of %s: %v", h, err)
		}

		sort.Strings(cs)
		sort.Strings(incls)
		if !reflect.DeepEqual(cs, incls) {
			t.Errorf("Head %s includers don't match: %v != %v", h, cs, incls)
		}
	}
}

func TestUptodateFile(t *testing.T) {
	sdb := openDB(t)
	defer closeDB(t, sdb)

	numHeaders := 10
	numCs := 100
	tudbs, _, _ := getRandTUDBs(t, numHeaders, numCs)

	half := len(tudbs) / 2
	added := tudbs[:half]
	notAdded := tudbs[half:]

	insertTUDBs(t, sdb, added)

	for _, tudb := range added {
		exist, _, err := sdb.UptodateFile(tudb.File)
		if err != nil {
			t.Errorf("Error calling UptodateFile: %v", err)
		}
		if exist == false {
			t.Errorf("Exist false on file added: %s", tudb.File)
		}
	}

	for _, tudb := range notAdded {
		exist, uptodate, err := sdb.UptodateFile(tudb.File)
		if err != nil {
			t.Errorf("Error calling UptodateFile: %v", err)
		}
		if exist == true {
			t.Errorf("Exist true on file added: %s", tudb.File)
		}
		if uptodate == true {
			t.Errorf("Uptodate true although file is newer: %s", tudb.File)
		}
	}
}

func TestRemoveFileReferences(t *testing.T) {
	sdb := openDB(t)
	defer closeDB(t, sdb)

	numHeaders := 10
	numCs := 100
	tudbs, headers, _ := getRandTUDBs(t, numHeaders, numCs)
	insertTUDBs(t, sdb, tudbs)

	for h, incls := range headers {
		for _, c := range incls {
			err := sdb.RemoveFileReferences(c)
			if err != nil {
				t.Errorf("Error removing reference of %s: %v", c, err)
			}
		}

		includers, err := sdb.GetIncluders(h)
		if err != badger.ErrKeyNotFound {
			t.Errorf("References of %s not successfully removed: %v. Files %v of %v left.", h, err, includers, incls)
		}
	}
}

func TestAddRemoveConcurrently(t *testing.T) {
	sdb := openDB(t)
	defer closeDB(t, sdb)

	numHeaders := 100
	numCs := 10000
	tudbs, _, _ := getRandTUDBs(t, numHeaders, numCs)

	half := len(tudbs) / 2
	toInsert := tudbs[half:]
	toRemove := tudbs[:half]

	insertTUDBs(t, sdb, toRemove)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		insertTUDBs(t, sdb, toInsert)
		wg.Done()
	}()

	go func() {
		removeTUDBs(t, sdb, toRemove)
		wg.Done()
	}()

	wg.Wait()
}
