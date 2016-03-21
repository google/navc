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
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
)

func main() {
	// path to symbols DB
	var dbDir string
	flag.StringVar(&dbDir, "db", ".navc_dbsymbols", "Path to symbols DB dir")

	// number of parallel indexing threads
	var nIndexingThreads int
	flag.IntVar(&nIndexingThreads, "numThreads", runtime.NumCPU(),
		"Number of indexing threads")

	// reset DB
	var resetDB bool
	flag.BoolVar(&resetDB, "resetDB", false,
		"Reset symbols DB and start over")

	flag.Parse()

	// list of directores with source to index
	var indexDir []string
	if len(flag.Args()) > 0 {
		indexDir = flag.Args()
		for _, path := range indexDir {
			fi, err := os.Stat(path)
			if err != nil {
				log.Println("unable to access ", path, err)
				return
			}
			if !fi.IsDir() {
				log.Println("only dir inputs allowed")
				return
			}
		}
	} else {
		indexDir = []string{"."}
	}

	// handle interrup and kill signals
	intr := make(chan os.Signal, 1)
	signal.Notify(intr, os.Interrupt, os.Kill)
	defer close(intr)

	// if we need to reset the database, erase the old one
	if resetDB {
		os.RemoveAll(dbDir)
	}

	// start files handler
	err := StartFilesHandler(indexDir, nIndexingThreads, dbDir)
	if err != nil {
		log.Println("unable to start daemon", err)
		return
	}
	defer CloseFilesHandler()

	// wait until ctl-c is pressed
	select {
	case <-intr:
	}
}
