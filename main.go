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
    "log"
    "flag"
    "runtime"
    "os"
    "regexp"
    fsnotify "gopkg.in/fsnotify.v1"
)

//TODO: How are we handling removed files?

func processFile(files chan string, db *SymbolsDB) {
    for {
        file := <-files
        log.Println("exploring", file)
        if !db.CheckUpToDate(file) {
            /* if it is not up to date, we need to remove all the references
             * to the old file and start over.
             */
            db.RemoveFileReferences(file)
            Parse(file, db)
        }
    }
}

func exploreDirsToIndex(toExplore []string, visitDir func(string),
                        visitC func(string)) error {
    for len(toExplore) > 0 {
        // dequeue first path
        path := toExplore[0]
        toExplore = toExplore[1:]

        f, err := os.Open(path)
        if err != nil {
            return err
        }

        info, err := f.Stat()
        if err != nil {
            return err
        }

        // visit file
        if !info.IsDir() {
            // ignore non-C files
            validC, _ := regexp.MatchString(`.*\.[ch]`, path)
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
            return err
        }

        for _, subf := range dirFiles {
            // ignore hidden files
            if subf.Name()[0] == '.' {
                continue
            }

            relPath := path + "/" + subf.Name()

            toExplore = append(toExplore, relPath)
        }
    }

    return nil
}

func main() {
    // path to symbols DB
    var dbFile string
    flag.StringVar(&dbFile, "db", ".dbsymbols", "Path to symbols path")

    // number of parallel indexing threads
    var nIndexingThreads int
    flag.IntVar(&nIndexingThreads, "numThread", runtime.NumCPU(),
                "Number of threads indexing")

    flag.Parse()

    // list of directores with source to index
    var indexDir []string
    if len(flag.Args()) > 0 {
        indexDir = flag.Args()
    } else {
        indexDir = []string{"."}
    }

    // open databased of symbols
    db, err := OpenSymbolsDB(dbFile)
    if err != nil {
        log.Fatal(err)
    }

    // start threads to process files
    files := make(chan string, nIndexingThreads)
    for i := 0; i < nIndexingThreads; i++ {
        go processFile(files, db)
    }

    // explore all the directories in indexDir and process all files
    watcher, _ := fsnotify.NewWatcher()
    visitorDir := func(path string) {
        // add watcher to directory
        watcher.Add(path)
    }
    visitorC := func(path string) {
        // put file in channel
        files <- path
        // TODO: update set to know what files to remove
        // add watcher
        watcher.Add(path)
    }
    exploreDirsToIndex(indexDir, visitorDir, visitorC)

    /*
    TODO: working example of fsnotify
    go func() {
        for {
            select {
                case event := <-watcher.Events:
                log.Println("event", event, event.Name)
            }
        }
    }()
    */

    /*
    funs, _ := db.GetFunctions("main")
    for _, f := range funs {
        log.Println(f)
    }
    */

    // TODO: wait for threads to finish
    done := make(chan int)
    <-done
}
