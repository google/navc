package main

import (
    "log"
)

func main() {
    db, err := OpenSymbolsDB(".dbsymbols")
    if err != nil {
        log.Fatal(err)
    }

    Parse("sample.c", db)

    funs, _ := db.GetFunctions("main")
    for _, f := range funs {
        log.Println(f)
    }
}
