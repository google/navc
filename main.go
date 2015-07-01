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

/* TODO: We have a problem with file dependencies. What if header files change?
 * All files dependent to this one should also be updated recursively. What
 * if the header is removed? Where would all the symbols go? What if the header
 * shows up again?
 *
 * We need to solve all this issues and it may require plenty of changes in the
 * symbols DB.
 */

package main

import (
	"flag"
	fsnotify "gopkg.in/fsnotify.v1"
	"log"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
)

func processFile(files chan string, wg *sync.WaitGroup, db *SymbolsDB) {
	wg.Add(1)
	defer wg.Done()

	// start exploring files
	for {
		file, ok := <-files

		if !ok {
			return
		}

		log.Println("exploring", file)
		Parse(file, db)
	}
}

func explorePathToParse(path string,
	visitDir func(string),
	visitC func(string)) {
	path = filepath.Clean(path)
	toExplore := []string{path}
	for len(toExplore) > 0 {
		// dequeue first path
		path := toExplore[0]
		toExplore = toExplore[1:]

		f, err := os.Open(path)
		if err != nil {
			log.Println(err, "opening", path, ", ignoring")
			continue
		}

		info, err := f.Stat()
		if err != nil {
			log.Println(err, "stating", path, ", ignoring")
			continue
		}

		// visit file
		if !info.IsDir() {
			// ignore non-C files
			validC, _ := regexp.MatchString(`.*\.[ch]$`, path)
			if validC {
				visitC(path)
			}
			continue
		} else {
			visitDir(path)
		}

		// add all the files in the directory to explore
		dirFiles, err := f.Readdir(0)
		if err != nil {
			log.Println(err, " readdir ", path, ", ignoring")
			continue
		}

		for _, subf := range dirFiles {
			// ignore hidden files
			if subf.Name()[0] == '.' {
				continue
			}

			relPath := path + "/" + subf.Name()
			relPath = filepath.Clean(relPath)

			toExplore = append(toExplore, relPath)
		}
	}
}

func handleChange(event fsnotify.Event,
	db *SymbolsDB,
	watcher *fsnotify.Watcher,
	files chan string) {

	visitorDir := func(path string) {
		// add watcher to directory
		watcher.Add(path)
	}
	visitorC := func(path string) {
		// add watcher
		watcher.Add(path)
		// put file in channel
		files <- path
	}

	switch {
	case event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Chmod) != 0:
		explorePathToParse(event.Name, visitorDir, visitorC)
	case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
		watcher.Remove(event.Name)
		tx := db.BeginTx()
		tx.RemoveFileReferences(event.Name)
		tx.Close()
	}
}

func main() {
	// path to symbols DB
	var dbFile string
	flag.StringVar(&dbFile, "db", ".navc_dbsymbols", "Path to symbols path")

	// number of parallel indexing threads
	var nIndexingThreads int
	flag.IntVar(&nIndexingThreads, "numThreads", 1,
		"Number of indexing threads (don't use)")

	// reset DB
	var resetDb bool
	flag.BoolVar(&resetDb, "resetDb", false, "Reset symbols DB and start over")

	flag.Parse()

	// socket file for communication with daemon
	socketFile := "/tmp/navc.sock"

	// list of directores with source to index
	var indexDir []string
	if len(flag.Args()) > 0 {
		indexDir = flag.Args()
	} else {
		indexDir = []string{"."}
	}

	// handle interrup and kill signals
	intr := make(chan os.Signal, 1)
	signal.Notify(intr, os.Interrupt, os.Kill)

	var wg sync.WaitGroup
	defer wg.Wait()

	// if we need to reset the database, erase the old one
	if resetDb {
		os.Remove(dbFile)

	}

	// open databased of symbols
	db := OpenSymbolsDB(dbFile)
	defer db.Close()

	// start indexing threads
	files := make(chan string, nIndexingThreads)
	defer close(files)

	// start threads to process files
	for i := 0; i < nIndexingThreads; i++ {
		go processFile(files, &wg, db)
	}

	// start file watcher
	watcher, _ := fsnotify.NewWatcher()
	defer watcher.Close()
	go func() {
		wg.Add(1)
		defer wg.Done()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				handleChange(event, db, watcher, files)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}

				log.Println("watcher error: ", err)
			}
		}
	}()

	// explore all the paths in indexDir and process all files
	removedFilesSet := db.GetSetFilesInDB()
	visitorDir := func(path string) {
		// add watcher to directory
		watcher.Add(path)
	}
	visitorC := func(path string) {
		// update set of removed files
		delete(removedFilesSet, path)
		// add watcher
		watcher.Add(path)
		// put file in channel
		files <- path
	}
	for _, path := range indexDir {
		explorePathToParse(path, visitorDir, visitorC)
	}

	// remove from DB deleted files
	tx := db.BeginTx()
	for path := range removedFilesSet {
		tx.RemoveFileReferences(path)
	}
	tx.Close()

	// start serving requests
	os.Remove(socketFile)
	lis, err := net.Listen("unix", socketFile)
	if err != nil {
		log.Println("error opening socket", err)
		return
	}
	defer os.Remove(socketFile)
	defer lis.Close()

	handler := rpc.NewServer()
	handler.Register(&RequestHandler{db})
	go func() {
		wg.Add(1)
		defer wg.Done()

		for {
			conn, err := lis.Accept()
			if err != nil {
				log.Println("accepting connection (breaking):", err)
				return
			}

			codec := jsonrpc.NewServerCodec(conn)
			err = handler.ServeRequest(codec)
			if err != nil {
				log.Println("handling request (ignoring):", err)
			}
			codec.Close()
		}
	}()

	// wait until ctl-c is pressed
	select {
	case <-intr:
	}
}
