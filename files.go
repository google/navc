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

/* This file will handle all file related business. It will explore the file
 * system for new files and will watch files for changes. In particular, this
 * file is the gate to the files table in the database. Other components (Parser)
 * should rely on this file to know if a file was explored or not. */

import (
	fsnotify "gopkg.in/fsnotify.v1"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

const validCString string = `^[^\.].*\.c$`
const validHString string = `^[^\.].*\.h$`

var files chan string
var wg sync.WaitGroup
var watcher *fsnotify.Watcher
var writer chan *WriterDB

func uptodateFile(file string) bool {
	wr := <-writer
	defer func() { writer <- wr }()

	exist, uptodate, fi, err := wr.UptodateFile(file)

	if err != nil {
		// if there is an error with the dependency, we are going to
		// pretend everything is fine so the parser is not executed
		return true
	}

	if exist && uptodate {
		return true
	} else {
		wr.RemoveFileReferences(file)
		wr.InsertFile(file, fi)
		return false
	}
}

func processFile(parser *Parser) {
	wg.Add(1)
	defer wg.Done()

	// start exploring files
	for {
		file, ok := <-files

		if !ok {
			return
		}

		if !uptodateFile(file) {
			log.Println("parsing", file)
			parser.Parse(file)
		}
	}
}

func traversePath(path string, visitDir func(string), visitC func(string)) {
	filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Println("error opening ", path, " igoring")
			return filepath.SkipDir
		}

		// visit file
		if info.IsDir() {
			if info.Name()[0] == '.' {
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
			}
		}

		return nil
	})
}

func removeFileAndReparseDepends(file string, db *WriterDB) {
	deps := db.RemoveFileDepsReferences(file)
	db.RemoveFileReferences(file)

	for _, d := range deps {
		files <- d
	}
}

func handleFileChange(event fsnotify.Event) {

	validC, _ := regexp.MatchString(validCString, event.Name)
	validH, _ := regexp.MatchString(validHString, event.Name)

	switch {
	case validC:
		files <- event.Name
	case validH:
		db := <-writer
		exist, uptodate, _, err := db.UptodateFile(event.Name)

		if err != nil || (exist && !uptodate) {
			removeFileAndReparseDepends(filepath.Clean(event.Name), db)
		}

		writer <- db
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
			files <- path
		}
		traversePath(event.Name, visitorDir, visitorC)
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

func StartFilesHandler(indexDir []string, nIndexingThreads int, parser *Parser,
	db *DBConnFactory) {

	files = make(chan string, nIndexingThreads)
	writer = make(chan *WriterDB, 1)
	writer <- db.NewWriter()

	// start threads to process files
	for i := 0; i < nIndexingThreads; i++ {
		go processFile(parser)
	}

	// start file watcher
	watcher, _ = fsnotify.NewWatcher()
	go func() {
		wg.Add(1)
		defer wg.Done()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				handleChange(event)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}

				log.Println("watcher error: ", err)
			}
		}
	}()

	// explore all the paths in indexDir and process all files
	rd := db.NewReader()
	removedFilesSet := rd.GetSetFilesInDB()
	rd.Close()
	visitorDir := func(path string) {
		// add watcher to directory
		watcher.Add(path)
	}
	visitorC := func(path string) {
		// update set of removed files
		delete(removedFilesSet, path)
		// put file in channel
		files <- path
	}
	for _, path := range indexDir {
		traversePath(path, visitorDir, visitorC)
	}

	// remove from DB deleted files
	wr := <-writer
	for path := range removedFilesSet {
		wr.RemoveFileReferences(path)
	}
	writer <- wr
}

func UpdateDependency(file, dep string) bool {
	wr := <-writer
	defer func() { writer <- wr }()

	exist, uptodate, fi, err := wr.UptodateFile(dep)

	if err != nil {
		// if there is an error with the dependency, we are going to
		// pretend everything is fine so the parser move forward
		return true
	}

	if !exist {
		wr.InsertFile(dep, fi)
	} else if !uptodate {
		removeFileAndReparseDepends(dep, wr)
		files <- file
		return false
	}

	wr.InsertDependency(file, dep)
	return true
}

func CloseFilesHandler() {
	close(files)

	wr := <-writer
	wr.Close()
	close(writer)

	watcher.Close()

	wg.Wait()
}
