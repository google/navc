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
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
    "log"
)

type Function struct {
    name    string
    file    string
    line    int
    col     int
}

//TODO: we need destructors to close all statements and open DB
type SymbolsDB struct {
    db          *sql.DB

    insertFile  *sql.Stmt
    insertFunc  *sql.Stmt
    selectFunc  *sql.Stmt
    delFileRef  *sql.Stmt
}

func (db *SymbolsDB) empty() bool {
    rows, err := db.db.Query(`SELECT name FROM sqlite_master
                            WHERE type='table' AND name='files'`)
    if err != nil {
        log.Fatal(err)
    }

    return !rows.Next()
}

func (db *SymbolsDB) initDB() {
    initStmt := `
        CREATE TABLE files (
            id      INTEGER,
            hash    BLOB UNIQUE,
            path    TEXT UNIQUE,
            PRIMARY KEY(id)
        );
        CREATE TABLE func_defs (
            name    TEXT,
            file    INTEGER,
            line    INTEGER,
            col     INTEGER,
            PRIMARY KEY(name, file),
            FOREIGN KEY(file) REFERENCES files(id) ON DELETE CASCADE
        );
    `
    _, err := db.db.Exec(initStmt)
    if err != nil {
        log.Fatal("init", err)
    }
}

func OpenSymbolsDB(path string) (*SymbolsDB, error) {
    db, err := sql.Open("sqlite3", path)
    if err != nil {
        return nil, err
    }

    r := &SymbolsDB{db: db}

    db.Exec(`PRAGMA foreign_keys = ON;`)

    if r.empty() {
        r.initDB()
    }

    insertFile, err := db.Prepare(`
        INSERT INTO files(path, hash) VALUES (?, ?);
    `)
    if err != nil {
        return nil, err
    }
    r.insertFile = insertFile

    insertFunc, err := db.Prepare(`
        INSERT INTO func_defs(name, file, line, col)
            SELECT ?, id, ?, ? FROM files
            WHERE path = ?;
    `)
    if err != nil {
        return nil, err
    }
    r.insertFunc = insertFunc

    selectFunc, err := db.Prepare(`
        SELECT name, path, line, col FROM func_defs, files
        WHERE name = ? AND id = file;
    `)
    if err != nil {
        return nil, err
    }
    r.selectFunc = selectFunc

    delFileRef, err := db.Prepare(`
        DELETE FROM files WHERE path = ?;
    `)
    r.delFileRef = delFileRef

    return r, nil
}

func (db *SymbolsDB) InsertFile(path string) error {
    // TODO: calculate sha1
    _, err := db.insertFile.Exec(path, nil)
    if err != nil {
        return err
    }

    return nil
}

func (db *SymbolsDB) InsertFunction(fun *Function) error {
    _, err := db.insertFunc.Exec(fun.name, fun.line, fun.col, fun.file)
    if err != nil {
        return err
    }

    return nil
}

func (db *SymbolsDB) GetFunctions(name string) ([]*Function, error) {
    rs := make([]*Function, 0)

    r, err := db.selectFunc.Query(name)
    if err != nil {
        return nil, err
    }

    for r.Next() {
        f := new(Function)

        err = r.Scan(&f.name, &f.file, &f.line, &f.col)
        if err != nil {
            return nil, err
        }

        rs = append(rs, f)
    }

    return rs, nil
}

func (db *SymbolsDB) CheckUpToDate(file string) bool {
    //TODO: check if this file exist and is up to date. If not, return false.
    return false
}

func (db *SymbolsDB) RemoveFileReferences(file string) error {
    _, err := db.delFileRef.Exec(file)
    if err != nil {
        return err
    }

    return nil
}
