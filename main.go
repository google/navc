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
