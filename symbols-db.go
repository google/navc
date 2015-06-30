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
	"bytes"
	"database/sql"
	"encoding/binary"
	sqlite "github.com/mattn/go-sqlite3"
	"log"
	"os"
	"sync"
	"time"
)

type Symbol struct {
	Name  string
	Unisr string
	File  string
	Line  int
	Col   int
}

type SymbolsDB struct {
	db      *sql.DB
	dbLite  *sqlite.SQLiteConn
	ddb     *sql.DB
	ddbLite *sqlite.SQLiteConn

	wg     sync.WaitGroup
	ticker <-chan time.Time
	flush  chan interface{}

	insertFile       *sql.Stmt
	selectFileInfo   *sql.Stmt
	insertSymb       *sql.Stmt
	insertFuncDef    *sql.Stmt
	insertFuncDecDef *sql.Stmt
	insertSymbUse    *sql.Stmt
	insertFuncCall   *sql.Stmt
	delFileRef       *sql.Stmt
	selectSymbDecl   *sql.Stmt
	selectSymbUses   *sql.Stmt
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
            path        TEXT UNIQUE NOT NULL,
            PRIMARY     KEY(id)
        );
        CREATE TABLE symbol_decls (
            name    TEXT NOT NULL,
            unisr   TEXT NOT NULL,
            file    INTEGER,
            line    INTEGER,
            col     INTEGER,

            param   INTEGER DEFAULT 0,

            def     INTEGER DEFAULT 0,

            PRIMARY KEY(file, line, col)
            FOREIGN KEY(file) REFERENCES files(id) ON DELETE CASCADE
        );
        CREATE TABLE func_decs_defs (
            dec_file    INTEGER,
            dec_line    INTEGER,
            dec_col     INTEGER,

            def_file    INTEGER,
            def_line    INTEGER,
            def_col     INTEGER,

            PRIMARY KEY(dec_file, dec_line, dec_col,
                        def_file, dec_line, dec_col)

            FOREIGN KEY(dec_file, dec_line, dec_col)
                REFERENCES symbol_decls(file, line, col) ON DELETE CASCADE
            FOREIGN KEY(def_file, def_line, def_col)
                REFERENCES symbol_decls(file, line, col) ON DELETE CASCADE
        );
        CREATE TABLE symbol_uses (
            call        INTEGER DEFAULT 0,

            file        INTEGER,
            line        INTEGER,
            col         INTEGER,

            dec_file    INTEGER,
            dec_line    INTEGER,
            dec_col     INTEGER,

            PRIMARY KEY(file, line, col)

            FOREIGN KEY(dec_file, dec_line, dec_col)
                REFERENCES symbol_decls(file, line, col) ON DELETE CASCADE
        );
	`
	_, err := db.db.Exec(initStmt)
	if err != nil {
		log.Fatal("init db ", err)
	}
}

func copyDb(src *sqlite.SQLiteConn, dst *sqlite.SQLiteConn) {
	backup, err := dst.Backup("main", src, "main")
	if err != nil {
		return
	}
	defer backup.Finish()

	backup.Step(-1)
}

func (db *SymbolsDB) flusher() {
	db.wg.Add(1)
	defer db.wg.Done()

	db.ticker = time.Tick(30 * time.Second)
	db.flush = make(chan interface{}, 1)

	for {
		select {
		case <-db.ticker:
			// if this takes too long, we need to flush piece-wise
			copyDb(db.dbLite, db.ddbLite)
		case _, ok := <-db.flush:
			copyDb(db.dbLite, db.ddbLite)
			if !ok {
				return
			}
		}
	}
}

func OpenSymbolsDB(path string) *SymbolsDB {
	/*
	 * We need two DB connections in the symbol DB: one for the in memory
	 * (main) database, and one for the in disk back database. Moreover, Go
	 * sql interface does not provide functions to get SQLiteConn, that are
	 * necessary to flow data from these two databases. For that reason, we
	 * register a new driver with a connection hook that catch both
	 * SQLiteConn for both DB connections.
	 */

	// open DB

	sql3Conn := []*sqlite.SQLiteConn{}
	sql.Register("sqlite3_conn_catch",
		&sqlite.SQLiteDriver{
			ConnectHook: func(newConn *sqlite.SQLiteConn) error {
				sql3Conn = append(sql3Conn, newConn)
				return nil
			},
		},
	)

	db, err := sql.Open("sqlite3_conn_catch", "file::memory:?cache=shared")
	if err != nil {
		log.Fatal("open db ", err)
	}
	db.Ping()

	ddb, err := sql.Open("sqlite3_conn_catch", path)
	if err != nil {
		log.Fatal("open ddb ", err)
	}
	ddb.Ping()

	r := &SymbolsDB{db: db, dbLite: sql3Conn[0], ddb: ddb, ddbLite: sql3Conn[1]}

	// init DB

	db.Exec(`PRAGMA foreign_keys = ON;`)

	copyDb(r.ddbLite, r.dbLite)

	if r.empty() {
		r.initDB()
	}

	go r.flusher()

	// DB inserts

	r.insertFile, err = db.Prepare(`
        INSERT INTO files(path, file_info) VALUES (?, ?);
	`)
	if err != nil {
		log.Fatal("prepare insert files ", err)
	}

	r.insertSymb, err = db.Prepare(`
        INSERT OR IGNORE INTO symbol_decls(name, unisr, file, line, col, param)
            SELECT ?, ?, id, ?, ?, ? FROM files
            WHERE path = ?;
	`)
	if err != nil {
		log.Fatal("prepare insert symbol ", err)
	}

	r.insertFuncDef, err = db.Prepare(`
        INSERT OR IGNORE INTO symbol_decls(name, unisr, file, line, col, def)
            SELECT ?, ?, id, ?, ?, 1 FROM files
            WHERE path = ?;
	`)
	if err != nil {
		log.Fatal("prepare insert func def ", err)
	}

	r.insertFuncDecDef, err = db.Prepare(`
        INSERT OR IGNORE INTO func_decs_defs
            SELECT f1.id, ?, ?, f2.id, ?, ? FROM files f1, files f2
            WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Fatal("prepare insert func dec/def ", err)
	}

	r.insertSymbUse, err = db.Prepare(`
        INSERT OR IGNORE INTO symbol_uses
            SELECT 0, f1.id, ?, ?, f2.id, ?, ? FROM files f1, files f2
                WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Fatal("preapre insert symbol use ", err)
	}

	r.insertFuncCall, err = db.Prepare(`
        INSERT OR REPLACE INTO symbol_uses
            SELECT 1, f1.id, ?, ?, f2.id, ?, ? FROM files f1, files f2
                WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Fatal("preapre insert func call ", err)
	}

	// DB (only) delete

	r.delFileRef, err = db.Prepare(`
        DELETE FROM files WHERE path = ?;
	`)
	if err != nil {
		log.Fatal("prepare delete file ", err)
	}

	// DB selects

	r.selectFileInfo, err = db.Prepare(`
        SELECT file_info FROM files WHERE path = ?;
	`)
	if err != nil {
		log.Fatal("prepare select hash ", err)
	}

	r.selectSymbDecl, err = db.Prepare(`
        SELECT st.name, st.unisr, f2.path, st.line, st.col
            FROM symbol_decls st, symbol_uses su, files f1, files f2
            WHERE
                -- symbol use and symbol declaration join
                su.dec_file = st.file AND
                su.dec_line = st.line AND
                su.dec_col = st.col AND
                -- symbol declaration to file join
                f2.id = st.file AND
                -- symbol use and file join
                su.file = f1.id AND
                -- select input
                f1.path = ? AND su.line = ? AND su.col = ?;
	`)
	if err != nil {
		log.Fatal("prepare select symbol ", err)
	}

	r.selectSymbUses, err = db.Prepare(`
        SELECT f2.path, su2.line, su2.col
            FROM files f1, files f2, symbol_uses su1, symbol_uses su2
            WHERE
                -- symbol use and files join
                f1.id = su1.file AND
                f2.id = su2.file AND
                -- symbol uses with same declaration
                su1.dec_file = su2.dec_file AND
                su1.dec_line = su2.dec_line AND
                su1.dec_col = su2.dec_col AND
                -- select input
                f1.path = ? AND su1.line = ? AND su1.col = ?;
	`)
	if err != nil {
		log.Fatal("prepare select symbol uses ", err)
	}

	return r
}

func (db *SymbolsDB) InsertFuncCall(call, dec *Symbol) {
	_, err := db.insertFuncCall.Exec(call.Line, call.Col,
		dec.Line, dec.Col,
		call.File, dec.File)
	if err != nil {
		log.Fatal("insert func call ", err)
	}
}

func (db *SymbolsDB) InsertSymbolUse(use, dec *Symbol) {
	_, err := db.insertSymbUse.Exec(use.Line, use.Col,
		dec.Line, dec.Col,
		use.File, dec.File)
	if err != nil {
		log.Fatal("insert symbol user ", err)
	}
}

func (db *SymbolsDB) InsertSymbol(sym *Symbol) {
	_, err := db.insertSymb.Exec(sym.Name, sym.Unisr,
		sym.Line, sym.Col, false, sym.File)
	if err != nil {
		log.Fatal("insert symbol ", err)
	}
}

func (db *SymbolsDB) InsertParamDecl(sym *Symbol) {
	_, err := db.insertSymb.Exec(sym.Name, sym.Unisr,
		sym.Line, sym.Col, true, sym.File)
	if err != nil {
		log.Fatal("insert symbol param ", err)
	}
}

func (db *SymbolsDB) GetSymbolDecl(use *Symbol) *Symbol {
	r, err := db.selectSymbDecl.Query(use.File, use.Line, use.Col)
	if err != nil {
		log.Fatal("select symbol decl ", err)
	}
	defer r.Close()

	if r.Next() {
		s := new(Symbol)

		err = r.Scan(&s.Name, &s.Unisr, &s.File, &s.Line, &s.Col)
		if err != nil {
			log.Fatal("scan symbol ", err)
		}

		return s
	} else {
		return nil
	}
}

func (db *SymbolsDB) GetSymbolUses(use *Symbol) []*Symbol {
	r, err := db.selectSymbUses.Query(use.File, use.Line, use.Col)
	if err != nil {
		log.Fatal("select symbol uses ", err)
	}
	defer r.Close()

	ret := []*Symbol{}
	for r.Next() {
		s := new(Symbol)

		err = r.Scan(&s.File, &s.Line, &s.Col)
		if err != nil {
			log.Fatal("scan symbol ", err)
		}

		ret = append(ret, s)
	}
	return ret
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

	data := []interface{}{
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
		sqliteErr := err.(sqlite.Error)
		if sqliteErr.ExtendedCode == sqlite.ErrConstraintUnique {
			// two threads tried to add the same file, fail the second one
			return false
		} else {
			log.Fatal("insert file ", err)
		}
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

func (db *SymbolsDB) InsertFuncDef(def *Symbol) {
	// insert function definition. Ignore if already exists.
	_, err := db.insertFuncDef.Exec(def.Name, def.Unisr,
		def.Line, def.Col, def.File)
	if err != nil {
		log.Fatal("insert func def ", err)
	}
}

func (db *SymbolsDB) InsertFuncSymb(dec, def *Symbol) {
	db.db.Query(`BEGIN TRANSACTION;`)

	db.InsertFuncDef(def)
	db.InsertSymbol(dec)

	// point this declaration to its definition
	_, err := db.insertFuncDecDef.Exec(
		dec.Line, dec.Col,
		def.Line, def.Col,
		dec.File, def.File)
	if err != nil {
		log.Fatal("insert func dec to def ", err)
	}

	db.db.Query(`COMMIT TRANSACTION;`)
}

func (db *SymbolsDB) Close() {
	db.insertFile.Close()
	db.insertSymb.Close()
	db.insertFuncDef.Close()
	db.insertFuncDecDef.Close()
	db.insertSymbUse.Close()
	db.insertFuncCall.Close()

	db.delFileRef.Close()

	db.selectFileInfo.Close()
	db.selectSymbDecl.Close()
	db.selectSymbUses.Close()

	close(db.flush)
	db.wg.Wait()

	db.ddb.Close()
	db.db.Close()
}
