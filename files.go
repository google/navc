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

/* TODO(useche): text explaining what is going on here */

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	fsnotify "gopkg.in/fsnotify.v1"
)

const validCString string = `^[^\.].*\.c$`
const validHString string = `^[^\.].*\.h$`
const flushTime int = 10

var sysInclDir map[string]bool = map[string]bool{
	"/usr/include/": true,
	"/usr/lib/":     true,
}

var parseFile chan string
var doneFile chan *TUSymbolsDB
var foundFile, foundHeader, removeFile chan string
var flush <-chan time.Time
var newConn chan net.Conn

var wg sync.WaitGroup
var watcher *fsnotify.Watcher

var db *SymbolsDB
var rh *RequestHandler

func traversePath(path string, visitDir func(string), visitC func(string), visitRest func(string)) {
	filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Println("error opening", path, "igoring", err)
			return filepath.SkipDir
		}

		// visit file
		if info.IsDir() {
			if info.Name() != "." && info.Name()[0] == '.' {
				return filepath.SkipDir
			} else {
				visitDir(path)
				return nil
			}
		} else {
			// ignore non-C files
			validC, _ := regexp.MatchString(validCString, path)
			if validC {
				visitC(path)
			} else {
				visitRest(path)
			}
		}

		return nil
	})
}

func queueFilesToParse(files ...string) {
	go func() {
		for _, f := range files {
			parseFile <- f
		}
	}()
}

func removeFileAndReparseDepends(file string) {
	deps, err := db.RemoveFileDepsReferences(file)
	if err != nil {
		log.Panic("unable to remove deps")
	}
	queueFilesToParse(deps...)
}

func handleFileChange(event fsnotify.Event) {
	validC, _ := regexp.MatchString(validCString, event.Name)
	validH, _ := regexp.MatchString(validHString, event.Name)
	path := filepath.Clean(event.Name)

	switch {
	case validC:
		switch {
		case event.Op&(fsnotify.Create|fsnotify.Write) != 0:
			queueFilesToParse(path)
		case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
			db.RemoveFileReferences(path)
		}
	case validH:
		exist, uptodate, err := db.UptodateFile(event.Name)
		switch {
		case err != nil:
			return
		case event.Op&(fsnotify.Write) != 0:
			if exist && !uptodate {
				removeFileAndReparseDepends(path)
			}
		case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
			if exist {
				removeFileAndReparseDepends(path)
			}
		}
	}
}

func handleDirChange(event fsnotify.Event) {
	switch {
	case event.Op&(fsnotify.Create) != 0:
		// explore the new dir
		visitorDir := func(path string) {
			// add watcher to directory
			watcher.Add(path)
		}
		visitorC := func(path string) {
			// put file in channel
			queueFilesToParse(path)
		}
		visitorRest := func(path string) {
			// nothing to do
		}
		traversePath(event.Name, visitorDir, visitorC, visitorRest)
	case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
		// remove watcher from dir
		watcher.Remove(event.Name)
	}
}

func isDirectory(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	} else {
		return fi.IsDir(), nil
	}
}

func handleChange(event fsnotify.Event) {

	// ignore if hidden
	if filepath.Base(event.Name)[0] == '.' {
		return
	}

	// first, we need to check if the file is a directory or not
	isDir, err := isDirectory(event.Name)
	if err != nil {
		// ignoring this event
		return
	}

	if isDir {
		handleDirChange(event)
	} else {
		handleFileChange(event)
	}
}

func isSysInclDir(path string) bool {
	for incl := range sysInclDir {
		if strings.HasPrefix(path, incl) {
			return true
		}
	}

	return false
}

func parseFiles(pa *Parser) {
	wg.Add(1)
	defer wg.Done()

	for file := range parseFile {
		log.Println("parsing", file)
		doneFile <- pa.Parse(file)
	}
}

func handleFiles() {
	wg.Add(1)
	defer wg.Done()

	for {
		select {
		// process parsed files
		case tudb, ok := <-doneFile:
			if !ok {
				return
			}
			db.InsertTUDB(tudb)
		// process changes in files
		case event := <-watcher.Events:
			handleChange(event)
		case err := <-watcher.Errors:
			log.Println("watcher error: ", err)
		// process explored files
		case header := <-foundHeader:
			exist, uptodate, err := db.UptodateFile(header)
			if err == nil && exist && !uptodate {
				removeFileAndReparseDepends(header)
			}
		case file := <-foundFile:
			exist, uptodate, err := db.UptodateFile(file)
			if err == nil && (!exist || !uptodate) {
				queueFilesToParse(file)
			}
		case file := <-removeFile:
			db.RemoveFileReferences(file)
		// flush frequently to disk
		case <-flush:
			db.FlushDB(time.Now().Add(-time.Duration(flushTime) * time.Second))
		// handle requests
		case conn := <-newConn:
			rh.handleRequest(conn)
		}
	}
}

func exploreIndexDir(indexDir []string) {
	wg.Add(1)
	defer wg.Done()

	// explore all the paths in indexDir and process all files
	notExplored := db.GetSetFilesInDB()
	visitorDir := func(path string) {
		// add watcher to directory
		watcher.Add(path)
	}
	visitorC := func(path string) {
		// update set of removed files
		delete(notExplored, path)
		// put file in channel
		foundFile <- path
	}
	visitorRest := func(path string) {
		if notExplored[path] {
			// update set of removed files
			delete(notExplored, path)

			foundHeader <- path
		}
	}
	for _, path := range indexDir {
		traversePath(path, visitorDir, visitorC, visitorRest)
	}

	// check files not explored by now
	for path := range notExplored {
		if isSysInclDir(path) {
			// if system include dir, visit normally
			visitorRest(path)
		} else {
			// if not, then delete
			removeFile <- path
		}
	}
}

func StartFilesHandler(indexDir []string, nIndexingThreads int, dbDir string) error {
	var err error

	parseFile = make(chan string)
	doneFile = make(chan *TUSymbolsDB)
	foundFile = make(chan string)
	foundHeader = make(chan string)
	removeFile = make(chan string)
	newConn = make(chan net.Conn)
	watcher, err = fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	flush = time.Tick(time.Duration(flushTime) * time.Second)
	db = NewSymbolsDB(dbDir)
	rh = NewRequestHandler(db)

	// start threads to process files
	for i := 0; i < nIndexingThreads; i++ {
		go parseFiles(NewParser(indexDir))
	}

	go ListenRequests(newConn)
	go handleFiles()
	go exploreIndexDir(indexDir)

	return nil
}

func CloseFilesHandler() {
	close(parseFile)
	close(doneFile)

	watcher.Close()

	wg.Wait()

	db.FlushDB(time.Now())
}
