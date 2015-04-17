package main

import (
    "fmt"
    "github.com/sbinet/go-clang"
)

func Parse(file string, db *SymbolsDB) {
    idx := clang.NewIndex(0, 1)
    defer idx.Dispose()

    args := []string{}

    tu := idx.Parse(file, args, nil, 0)
    defer tu.Dispose()

    visitNode := func (cursor, parent clang.Cursor) clang.ChildVisitResult {
        if cursor.IsNull() {
            return clang.CVR_Continue
        }

        fmt.Printf("%s: %s (%s)\n",
            cursor.Kind().Spelling(), cursor.Spelling(), cursor.USR())
        fName, line, col, _ := cursor.Location().GetFileLocation()
        fmt.Println(fName.Name(), ":", line, col)

        switch cursor.Kind() {
        case clang.CK_FunctionDecl:
            fun := &Function{cursor.Spelling(), fName.Name(), int(line), int(col)}
            db.InsertFunction(fun)
            //TODO: recurse when looking for variables and funtion calls
            return clang.CVR_Continue
        }
        return clang.CVR_Continue
    }

    tu.ToCursor().Visit(visitNode)
}
