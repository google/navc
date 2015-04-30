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

/* TODO: should we consider the case where multiple declarations and multiple
 * definitions exist for the same symbol? This could happen before C
 * preprocesor */

package main

import (
    "bytes"
    "database/sql"
    "encoding/binary"
    _ "github.com/mattn/go-sqlite3"
    "log"
    "os"
)

type Symbol struct {
    name    string
    file    string
    line    int
    col     int
}

type SymbolsDB struct {
    db              *sql.DB

    insertFile      *sql.Stmt
    selectFileInfo  *sql.Stmt
    insertSymb      *sql.Stmt
    selectSymb      *sql.Stmt
    delFileRef      *sql.Stmt
}

func (db *SymbolsDB) empty() bool {
    rows, err := db.db.Query(`SELECT name FROM sqlite_master
                            WHERE type='table' AND name='files';`)
    if err != nil {
        log.Fatal("check empty ", err)
    }
    defer rows.Close()

    return !rows.Next()
}

func (db *SymbolsDB) initDB() {
    initStmt := `
        CREATE TABLE files (
            id          INTEGER,
            file_info   BLOB,
            path        TEXT UNIQUE,
            PRIMARY     KEY(id)
        );
        CREATE TABLE symbol_decls (
            name    TEXT,
            file    INTEGER,
            line    INTEGER,
            col     INTEGER,
            PRIMARY KEY(name, file, line, col),
            FOREIGN KEY(file) REFERENCES files(id) ON DELETE CASCADE
        );
        CREATE TABLE func_defs (
            name        TEXT,
            file        INTEGER,
            line        INTEGER,
            col         INTEGER,

            def_file    INTEGER,
            def_line    INTEGER,
            def_col     INTEGER,

            PRIMARY KEY(name, file, line, col),
            FOREIGN KEY(file) REFERENCES files(id) ON DELETE CASCADE,

            FOREIGN KEY(name, def_file, def_line, def_col)
                REFERENCES symbol_decls(name, file, line, col) ON DELETE CASCADE
        );
    `
    _, err := db.db.Exec(initStmt)
    if err != nil {
        log.Fatal("init db ", err)
    }
}

func OpenSymbolsDB(path string) *SymbolsDB {
    db, err := sql.Open("sqlite3", path)
    if err != nil {
        log.Fatal("open db ", err)
    }

    r := &SymbolsDB{db: db}

    db.Exec(`PRAGMA foreign_keys = ON;`)

    if r.empty() {
        r.initDB()
    }

    r.insertFile, err = db.Prepare(`
        INSERT INTO files(path, file_info) VALUES (?, ?);
    `)
    if err != nil {
        log.Fatal("prepare insert files ", err)
    }

    r.selectFileInfo, err = db.Prepare(`
        SELECT file_info FROM files WHERE path = ?;
    `)
    if err != nil {
        log.Fatal("prepare select hash ", err)
    }

    r.insertSymb, err = db.Prepare(`
        INSERT INTO symbol_decls(name, file, line, col)
            SELECT ?, id, ?, ? FROM files
            WHERE path = ?;
    `)
    if err != nil {
        log.Fatal("prepare insert symbol ", err)
    }

    r.selectSymb, err = db.Prepare(`
        SELECT name, path, line, col FROM symbol_decls, files
        WHERE name = ? AND id = file;
    `)
    if err != nil {
        log.Fatal("prepare select symbol ", err)
    }

    r.delFileRef, err = db.Prepare(`
        DELETE FROM files WHERE path = ?;
    `)
    if err != nil {
        log.Fatal("prepare delete file ", err)
    }

    return r
}

func (db *SymbolsDB) InsertSymbol(sym *Symbol) {
    _, err := db.insertSymb.Exec(sym.name, sym.line, sym.col, sym.file)
    if err != nil {
        log.Fatal("insert symbol ", err)
    }
}

func (db *SymbolsDB) GetSymbols(name string) []*Symbol {
    r, err := db.selectSymb.Query(name)
    if err != nil {
        log.Fatal("select symbol ", err)
    }
    defer r.Close()

    rs := make([]*Symbol, 0)
    for r.Next() {
        s := new(Symbol)

        err = r.Scan(&s.name, &s.file, &s.line, &s.col)
        if err != nil {
            log.Fatal("scan symbol ", err)
        }

        rs = append(rs, s)
    }

    return rs
}

func getFileInfoBytes(fi os.FileInfo) []byte {
    timeBytes, err := fi.ModTime().MarshalBinary()
    if err != nil {
        log.Fatal("time to bytes ", err)
    }

    var dir byte
    if fi.IsDir() {
        dir = 1
    } else {
        dir = 0
    }

    var data = []interface{}{
        fi.Size(),
        fi.Mode(),
        timeBytes,
        dir,
    }
    buf := new(bytes.Buffer)
    for _, v := range data {
        err := binary.Write(buf, binary.BigEndian, v)
        if err != nil {
            log.Panic("getting bytes from FileInfo ", err)
        }
    }
    return buf.Bytes()
}

func (db *SymbolsDB) getFileInfoBytesDB(file string) (bool, []byte) {
    r, err := db.selectFileInfo.Query(file)
    if err != nil {
        log.Fatal("select file info ", err)
    }
    defer r.Close()

    if r.Next() {
        var inDbFileInfo []byte

        err := r.Scan(&inDbFileInfo)
        if err != nil {
            log.Fatal("scanning file info ", err)
        }

        return true, inDbFileInfo
    } else {
        return false, nil
    }

}

/*
 * This function checks if the file exist and it is up to date. If it is not
 * not up to date, it will remove the current references of the file in the DB.
 * In either case, it will insert a new file entry in the DB and the Parser
 * should be called to populate the DB with the new symbols.
 */
func (db *SymbolsDB) NeedToProcessFile(file string) bool {
    fi, err := os.Stat(file)
    if err != nil {
        log.Println(err, ": unable to read file ", file)
        db.RemoveFileReferences(file)
        return false
    }

    fiBytes := getFileInfoBytes(fi)
    exist, inDbFiBytes := db.getFileInfoBytesDB(file)

    if exist {
        if bytes.Compare(fiBytes, inDbFiBytes) == 0 {
            // the file info in the DB and the file are the same; nothing to process.
            return false
        } else {
            // not up to date, remove all references
            db.RemoveFileReferences(file)
        }
    }

    _, err = db.insertFile.Exec(file, fiBytes)
    if err != nil {
        log.Fatal("insert file ", err)
    }

    return true
}

func (db *SymbolsDB) RemoveFileReferences(file string) {
    _, err := db.delFileRef.Exec(file)
    if err != nil {
        log.Fatal("delete file ", err)
    }
}

func (db *SymbolsDB) GetSetFilesInDB() map[string]bool {
    rows, err := db.db.Query(`SELECT path FROM files;`)
    if err != nil {
        log.Fatal("select files ", err)
    }
    defer rows.Close()

    fileSet := map[string]bool{}
    for rows.Next() {
        var path string

        err := rows.Scan(&path)
        if err != nil {
            log.Fatal("scan path ", err)
        }

        fileSet[path] = true
    }

    return fileSet
}

func (db *SymbolsDB) Close() {
    db.insertFile.Close()
    db.selectFileInfo.Close()
    db.insertSymb.Close()
    db.selectSymb.Close()
    db.delFileRef.Close()
    db.db.Close()
}
