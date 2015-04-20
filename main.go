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
)

//TODO: How are we handling removed files?

func processFile(files chan string, db *SymbolsDB) {
    for {
        file := <-files
        if !db.CheckUpToDate(file) {
            /* if it is not up to date, we need to remove all the references
             * to the old file and start over.
             */
            db.RemoveFileReferences(file)
            Parse(file, db)
        }
    }
}

func main() {
    // path to symbols DB
    var dbFile string
    flag.StringVar(&dbFile, "db", ".dbsymbols", "Path to symbols path")

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
    files := make(chan string)
    for i := 0; i < runtime.NumCPU(); i++ {
        go processFile(files, db)
    }

    /*
    funs, _ := db.GetFunctions("main")
    for _, f := range funs {
        log.Println(f)
    }
    */
}
