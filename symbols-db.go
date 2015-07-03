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

	selectSymbDecl *sql.Stmt
	selectSymbUses *sql.Stmt
}

func (db *SymbolsDB) empty() bool {
	rows, err := db.db.Query(`SELECT name FROM sqlite_master
                            WHERE type='table' AND name='files';`)
	if err != nil {
		log.Panic("check empty ", err)
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
		log.Panic("init db ", err)
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
			// TODO: This needs to create a DB connection every
			// time it is used
			// copyDb(db.dbLite, db.ddbLite)
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
		log.Panic("open db ", err)
	}
	db.Ping()

	ddb, err := sql.Open("sqlite3_conn_catch", path)
	if err != nil {
		log.Panic("open ddb ", err)
	}
	ddb.Ping()

	r := &SymbolsDB{
		db: db, dbLite: sql3Conn[0],
		ddb: ddb, ddbLite: sql3Conn[1],
	}

	// init DB

	db.Exec(`PRAGMA foreign_keys = ON;`)

	copyDb(r.ddbLite, r.dbLite)

	if r.empty() {
		r.initDB()
	}

	go r.flusher()

	// DB selects

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
		log.Panic("prepare select symbol ", err)
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
		log.Panic("prepare select symbol uses ", err)
	}

	return r
}

func (db *SymbolsDB) GetSymbolDecl(use *Symbol) *Symbol {
	r, err := db.selectSymbDecl.Query(use.File, use.Line, use.Col)
	if err != nil {
		log.Panic("select symbol decl ", err)
	}
	defer r.Close()

	if r.Next() {
		s := new(Symbol)

		err = r.Scan(&s.Name, &s.Unisr, &s.File, &s.Line, &s.Col)
		if err != nil {
			log.Panic("scan symbol ", err)
		}

		return s
	} else {
		return nil
	}
}

func (db *SymbolsDB) GetSymbolUses(use *Symbol) []*Symbol {
	r, err := db.selectSymbUses.Query(use.File, use.Line, use.Col)
	if err != nil {
		log.Panic("select symbol uses ", err)
	}
	defer r.Close()

	ret := []*Symbol{}
	for r.Next() {
		s := new(Symbol)

		err = r.Scan(&s.File, &s.Line, &s.Col)
		if err != nil {
			log.Panic("scan symbol ", err)
		}

		ret = append(ret, s)
	}
	return ret
}

func (db *SymbolsDB) GetSetFilesInDB() map[string]bool {
	rows, err := db.db.Query(`SELECT path FROM files;`)
	if err != nil {
		log.Panic("select files ", err)
	}
	defer rows.Close()

	fileSet := map[string]bool{}
	for rows.Next() {
		var path string

		err := rows.Scan(&path)
		if err != nil {
			log.Panic("scan path ", err)
		}

		fileSet[path] = true
	}

	return fileSet
}

func (db *SymbolsDB) Close() {
	db.selectSymbDecl.Close()
	db.selectSymbUses.Close()

	close(db.flush)
	db.wg.Wait()

	db.ddb.Close()
	db.db.Close()
}

/*
 * Transactions will be used to modify the database. This include inserting
 * indexing info while parsing a file and deleting a file reference when no
 * longer exists. To modify the DB, a transaction should first be created and
 * once all the modifications are don, the trasaction should be closed.
 */

type SymbolsTx struct {
	tx *sql.Tx
	db *SymbolsDB

	selectFileInfo *sql.Stmt

	insertFile       *sql.Stmt
	insertSymb       *sql.Stmt
	insertFuncDef    *sql.Stmt
	insertFuncDecDef *sql.Stmt
	insertSymbUse    *sql.Stmt
	insertFuncCall   *sql.Stmt

	delFileRef *sql.Stmt
}

func (db *SymbolsDB) BeginTx() *SymbolsTx {
	tx, err := db.db.Begin()
	if err != nil {
		log.Panic("begin transaction ", err)
	}

	r := &SymbolsTx{db: db, tx: tx}

	// DB selects

	r.selectFileInfo, err = tx.Prepare(`
        SELECT file_info FROM files WHERE path = ?;
	`)
	if err != nil {
		log.Panic("prepare select hash ", err)
	}

	// DB inserts

	r.insertFile, err = tx.Prepare(`
        INSERT INTO files(path, file_info) VALUES (?, ?);
	`)
	if err != nil {
		log.Panic("prepare insert files ", err)
	}

	r.insertSymb, err = tx.Prepare(`
        INSERT OR IGNORE INTO symbol_decls(name, unisr, file, line, col, param)
            SELECT ?, ?, id, ?, ?, ? FROM files
            WHERE path = ?;
	`)
	if err != nil {
		log.Panic("prepare insert symbol ", err)
	}

	r.insertFuncDef, err = tx.Prepare(`
        INSERT OR IGNORE INTO symbol_decls(name, unisr, file, line, col, def)
            SELECT ?, ?, id, ?, ?, 1 FROM files
            WHERE path = ?;
	`)
	if err != nil {
		log.Panic("prepare insert func def ", err)
	}

	r.insertFuncDecDef, err = tx.Prepare(`
        INSERT OR IGNORE INTO func_decs_defs
            SELECT f1.id, ?, ?, f2.id, ?, ? FROM files f1, files f2
            WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Panic("prepare insert func dec/def ", err)
	}

	r.insertSymbUse, err = tx.Prepare(`
        INSERT OR IGNORE INTO symbol_uses
            SELECT 0, f1.id, ?, ?, f2.id, ?, ? FROM files f1, files f2
                WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Panic("preapre insert symbol use ", err)
	}

	r.insertFuncCall, err = tx.Prepare(`
        INSERT OR REPLACE INTO symbol_uses
            SELECT 1, f1.id, ?, ?, f2.id, ?, ? FROM files f1, files f2
                WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Panic("preapre insert func call ", err)
	}

	// DB (only) delete

	r.delFileRef, err = tx.Prepare(`
        DELETE FROM files WHERE path = ?;
	`)
	if err != nil {
		log.Panic("prepare delete file ", err)
	}

	return r
}

func (tx *SymbolsTx) InsertSymbol(sym *Symbol) {
	_, err := tx.insertSymb.Exec(sym.Name, sym.Unisr,
		sym.Line, sym.Col, false, sym.File)
	if err != nil {
		log.Panic("insert symbol ", err)
	}
}

func (tx *SymbolsTx) InsertParamDecl(sym *Symbol) {
	_, err := tx.insertSymb.Exec(sym.Name, sym.Unisr,
		sym.Line, sym.Col, true, sym.File)
	if err != nil {
		log.Panic("insert symbol param ", err)
	}
}

func (tx *SymbolsTx) InsertSymbolUse(use, dec *Symbol) {
	_, err := tx.insertSymbUse.Exec(use.Line, use.Col,
		dec.Line, dec.Col,
		use.File, dec.File)
	if err != nil {
		sqliteErr := err.(sqlite.Error)
		if sqliteErr.ExtendedCode == sqlite.ErrConstraintForeignKey {
			// If the symbol is not declared, ignore.
			//log.Println("use with no declaration ", use.Name, " ignoring")
		} else {
			log.Panic("insert symbol user ", err)
		}
	}
}

func (tx *SymbolsTx) InsertFuncCall(call, dec *Symbol) {
	_, err := tx.insertFuncCall.Exec(call.Line, call.Col,
		dec.Line, dec.Col,
		call.File, dec.File)
	if err != nil {
		sqliteErr := err.(sqlite.Error)
		if sqliteErr.ExtendedCode == sqlite.ErrConstraintForeignKey {
			// If the symbol is not declared, ignore.
			//log.Println("call with no declaration ", call.Name, " ignoring")
		} else {
			log.Panic("insert func call ", err)
		}
	}
}

func getFileInfoBytes(fi os.FileInfo) []byte {
	timeBytes, err := fi.ModTime().MarshalBinary()
	if err != nil {
		log.Panic("time to bytes ", err)
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

func (tx *SymbolsTx) getFileInfoBytesDB(file string) (bool, []byte) {
	var inDbFileInfo []byte

	err := tx.selectFileInfo.QueryRow(file).Scan(&inDbFileInfo)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		log.Panic("scanning file info ", err)
	default:
		return true, inDbFileInfo
	}

	return false, nil // not necessary but compiler complains
}

/*
 * This function checks if the file exist and it is up to date. If it is not
 * not up to date, it will remove the current references of the file in the DB.
 * In either case, it will insert a new file entry in the DB and the Parser
 * should be called to populate the DB with the new symbols.
 */
func (tx *SymbolsTx) NeedToProcessFile(file string) bool {
	fi, err := os.Stat(file)
	if err != nil {
		log.Println(err, ": unable to read file ", file)
		tx.RemoveFileReferences(file)
		return false
	}

	fiBytes := getFileInfoBytes(fi)
	exist, inDbFiBytes := tx.getFileInfoBytesDB(file)

	if exist {
		if bytes.Compare(fiBytes, inDbFiBytes) == 0 {
			// the file info in the DB and the file are the same; nothing to process.
			return false
		} else {
			// not up to date, remove all references
			tx.RemoveFileReferences(file)
		}
	}

	_, err = tx.insertFile.Exec(file, fiBytes)
	if err != nil {
		sqliteErr := err.(sqlite.Error)
		if sqliteErr.ExtendedCode == sqlite.ErrConstraintUnique {
			// two threads tried to add the same file, fail the second one
			return false
		} else {
			log.Panic("insert file ", err)
		}
	}

	return true
}

func (tx *SymbolsTx) RemoveFileReferences(file string) {
	_, err := tx.delFileRef.Exec(file)
	if err != nil {
		log.Panic("delete file ", err)
	}
}

func (tx *SymbolsTx) InsertFuncDef(def *Symbol) {
	// insert function definition. Ignore if already exists.
	_, err := tx.insertFuncDef.Exec(def.Name, def.Unisr, def.Line, def.Col,
		def.File)
	if err != nil {
		log.Panic("insert func def ", err)
	}
}

func (tx *SymbolsTx) InsertFuncSymb(dec, def *Symbol) {
	tx.InsertFuncDef(def)
	tx.InsertSymbol(dec)

	// point this declaration to its definition
	_, err := tx.insertFuncDecDef.Exec(
		dec.Line, dec.Col,
		def.Line, def.Col,
		dec.File, def.File)
	if err != nil {
		log.Panic("insert func dec to def ", err)
	}
}

func (tx *SymbolsTx) Close() {
	tx.selectFileInfo.Close()

	tx.insertFile.Close()
	tx.insertSymb.Close()
	tx.insertFuncDef.Close()
	tx.insertFuncDecDef.Close()
	tx.insertSymbUse.Close()
	tx.insertFuncCall.Close()

	tx.delFileRef.Close()

	tx.tx.Commit()
}
