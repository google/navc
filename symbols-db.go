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
	"encoding/binary"
	"github.com/mxk/go-sqlite/sqlite3"
	"io"
	"log"
	"os"
)

type Symbol struct {
	Name  string
	Unisr string
	File  string
	Line  int
	Col   int
}

type ReaderDB struct {
	conn *sqlite3.Conn

	selectSymbDecl *sqlite3.Stmt
	selectSymbUses *sqlite3.Stmt
	selectSymbDef  *sqlite3.Stmt
	selectSymbDefs *sqlite3.Stmt
}

func NewReaderDB(conn *sqlite3.Conn) *ReaderDB {
	var err error

	r := &ReaderDB{conn: conn}

	r.selectSymbDecl, err = conn.Prepare(`
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

	r.selectSymbUses, err = conn.Prepare(`
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

	r.selectSymbDef, err = conn.Prepare(`
	SELECT f1.path, fdd.def_line, fdd.def_col
	    FROM files f1, files f2, symbol_decls sd, symbol_uses su,
	         func_decs_defs fdd
	    WHERE
	        -- symbol decls and files join
		f1.id = fdd.def_file AND
		-- symbol use and files join
		f2.id = su.file AND
		-- symbol uses and symbol decls join
		su.dec_file = sd.file AND
		su.dec_line = sd.line AND
		su.dec_col = sd.col AND
		-- symbol decls and symbol defs join
		sd.file = fdd.dec_file AND
		sd.line = fdd.dec_line AND
		sd.col = fdd.dec_col AND
		-- select input
		f2.path = ? AND su.line = ? AND su.col = ?;
	`)
	if err != nil {
		log.Panic("prepare select symbol def ", err)
	}

	r.selectSymbDefs, err = conn.Prepare(`
	SELECT f1.path, sd1.line, sd1.col
	    FROM files f1, files f2, symbol_decls sd1, symbol_decls sd2,
	         symbol_uses su
	    WHERE
	        -- symbol decls and files join
		f1.id = sd1.file AND
		-- file and symbol uses join
		f2.id = su.file AND
		-- symbol uses and symbol decls join
		su.dec_file = sd2.file AND
		su.dec_line = sd2.line AND
		su.dec_col = sd2.col AND
		-- symbol decls and symbol decls same USR
		sd1.unisr = sd2.unisr AND
		sd1.def = 1 AND
		-- select input
		f2.path = ? AND su.line = ? AND su.col = ?;
	`)
	if err != nil {
		log.Panic("prepare select symbol defs ", err)
	}

	return r
}

func (db *ReaderDB) GetSymbolDecl(use *Symbol) *Symbol {
	err := db.selectSymbDecl.Query(use.File, use.Line, use.Col)
	switch {
	case err == io.EOF:
		return nil
	case err != nil:
		log.Panic("select symbol decl ", err)
	}
	defer db.selectSymbDecl.Reset()

	s := new(Symbol)

	err = db.selectSymbDecl.Scan(&s.Name, &s.Unisr, &s.File, &s.Line, &s.Col)
	if err != nil {
		log.Panic("scan symbol ", err)
	}

	return s
}

func (db *ReaderDB) GetSymbolUses(use *Symbol) []*Symbol {
	ret := []*Symbol{}

	err := db.selectSymbUses.Query(use.File, use.Line, use.Col)
	switch {
	case err == io.EOF:
		return ret
	case err != nil:
		log.Panic("select symbol uses ", err)
	}
	defer db.selectSymbUses.Reset()

	for {
		s := new(Symbol)

		err = db.selectSymbUses.Scan(&s.File, &s.Line, &s.Col)
		if err != nil {
			log.Panic("scan symbol ", err)
		}

		ret = append(ret, s)

		if db.selectSymbUses.Next() == io.EOF {
			break
		}
	}
	return ret
}

func (db *ReaderDB) GetSetFilesInDB() map[string]bool {
	fileSet := map[string]bool{}

	stmt, err := db.conn.Query(`SELECT path FROM files;`)
	switch {
	case err == io.EOF:
		return nil
	case err != nil:
		log.Panic("select files ", err)
	}

	for {
		var path string

		err := stmt.Scan(&path)
		if err != nil {
			log.Panic("scan path ", err)
		}

		fileSet[path] = true

		if stmt.Next() == io.EOF {
			break
		}
	}

	return fileSet
}

func (db *ReaderDB) GetSymbolDef(use *Symbol) *Symbol {
	err := db.selectSymbDef.Query(use.File, use.Line, use.Col)
	switch {
	case err == io.EOF:
		return nil
	case err != nil:
		log.Panic("select symbol def ", err)
	}
	defer db.selectSymbDef.Reset()

	s := new(Symbol)

	err = db.selectSymbDef.Scan(&s.File, &s.Line, &s.Col)
	if err != nil {
		log.Panic("scan symbol ", err)
	}

	return s
}

func (db *ReaderDB) GetAllSymbolDefs(use *Symbol) []*Symbol {
	ret := []*Symbol{}

	err := db.selectSymbDefs.Query(use.File, use.Line, use.Col)
	switch {
	case err == io.EOF:
		return ret
	case err != nil:
		log.Panic("select symbol uses ", err)
	}
	defer db.selectSymbDefs.Reset()

	for {
		s := new(Symbol)

		err = db.selectSymbDefs.Scan(&s.File, &s.Line, &s.Col)
		if err != nil {
			log.Panic("scan symbol ", err)
		}

		ret = append(ret, s)

		if db.selectSymbDefs.Next() == io.EOF {
			break
		}
	}
	return ret
}

func (db *ReaderDB) Close() {
	db.selectSymbDecl.Close()
	db.selectSymbUses.Close()
	db.selectSymbDef.Close()
	db.selectSymbDefs.Close()

	db.conn.Close()
}

type WriterDB struct {
	conn *sqlite3.Conn

	selectFileInfo *sqlite3.Stmt
	selectFileDeps *sqlite3.Stmt

	insertFile       *sqlite3.Stmt
	insertSymb       *sqlite3.Stmt
	insertFuncDef    *sqlite3.Stmt
	insertFuncDecDef *sqlite3.Stmt
	insertSymbUse    *sqlite3.Stmt
	insertFuncCall   *sqlite3.Stmt
	insertDepend     *sqlite3.Stmt

	delFileRef *sqlite3.Stmt
	delDepends *sqlite3.Stmt
}

func NewWriterDB(conn *sqlite3.Conn) *WriterDB {
	var err error

	r := &WriterDB{conn: conn}

	// DB selects

	r.selectFileInfo, err = conn.Prepare(`
        SELECT file_info FROM files WHERE path = ?;
	`)
	if err != nil {
		log.Panic("prepare select hash ", err)
	}

	r.selectFileDeps, err = conn.Prepare(`
        SELECT f1.path FROM files f1, files f2, files_deps d
	WHERE f2.path = ? AND f2.id = d.depend AND f1.id = d.id;
	`)
	if err != nil {
		log.Panic("prepare select hash ", err)
	}

	// DB inserts

	r.insertFile, err = conn.Prepare(`
        INSERT INTO files(path, file_info) VALUES (?, ?);
	`)
	if err != nil {
		log.Panic("prepare insert files ", err)
	}

	r.insertSymb, err = conn.Prepare(`
        INSERT OR IGNORE INTO symbol_decls(name, unisr, file, line, col, param)
            SELECT ?, ?, id, ?, ?, ? FROM files
            WHERE path = ?;
	`)
	if err != nil {
		log.Panic("prepare insert symbol ", err)
	}

	r.insertFuncDef, err = conn.Prepare(`
        INSERT OR IGNORE INTO symbol_decls(name, unisr, file, line, col, def)
            SELECT ?, ?, id, ?, ?, 1 FROM files
            WHERE path = ?;
	`)
	if err != nil {
		log.Panic("prepare insert func def ", err)
	}

	r.insertFuncDecDef, err = conn.Prepare(`
        INSERT OR IGNORE INTO func_decs_defs
            SELECT f1.id, ?, ?, f2.id, ?, ? FROM files f1, files f2
            WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Panic("prepare insert func dec/def ", err)
	}

	r.insertSymbUse, err = conn.Prepare(`
        INSERT OR IGNORE INTO symbol_uses
            SELECT 0, f1.id, ?, ?, f2.id, ?, ? FROM files f1, files f2
                WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Panic("preapre insert symbol use ", err)
	}

	r.insertFuncCall, err = conn.Prepare(`
        INSERT OR REPLACE INTO symbol_uses
            SELECT 1, f1.id, ?, ?, f2.id, ?, ? FROM files f1, files f2
                WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Panic("preapre insert func call ", err)
	}

	r.insertDepend, err = conn.Prepare(`
	INSERT OR IGNORE INTO files_deps
	    SELECT f1.id, f2.id FROM files f1, files f2
	        WHERE f1.path = ? AND f2.path = ?;
	`)
	if err != nil {
		log.Panic("prepare insert files deps ", err)
	}

	// DB (only) delete

	r.delFileRef, err = conn.Prepare(`
        DELETE FROM files WHERE path = ?;
	`)
	if err != nil {
		log.Panic("prepare delete file ", err)
	}

	r.delDepends, err = conn.Prepare(`
	DELETE FROM files WHERE id IN (
	    SELECT d.id FROM files_deps d, files f
	        WHERE f.path = ? AND d.depend = f.id
	);
	`)
	if err != nil {
		log.Panic("prepare delete dependencies ", err)
	}

	return r
}

func (db *WriterDB) InsertSymbol(sym *Symbol) {
	err := db.insertSymb.Exec(sym.Name, sym.Unisr,
		sym.Line, sym.Col, false, sym.File)
	if err != nil {
		log.Panic("insert symbol ", err)
	}
}

func (db *WriterDB) InsertParamDecl(sym *Symbol) {
	err := db.insertSymb.Exec(sym.Name, sym.Unisr,
		sym.Line, sym.Col, true, sym.File)
	if err != nil {
		log.Panic("insert symbol param ", err)
	}
}

func (db *WriterDB) InsertSymbolUse(use, dec *Symbol) {
	err := db.insertSymbUse.Exec(use.Line, use.Col,
		dec.Line, dec.Col,
		use.File, dec.File)
	if err != nil {
		sqliteErr := err.(*sqlite3.Error)
		if sqliteErr.Code() == sqlite3.CONSTRAINT_FOREIGNKEY {
			// If the symbol is not declared, ignore.
			//log.Println("use with no declaration ", use.Name, " ignoring")
		} else {
			log.Panic("insert symbol user ", err)
		}
	}
}

func (db *WriterDB) InsertFuncCall(call, dec *Symbol) {
	err := db.insertFuncCall.Exec(call.Line, call.Col,
		dec.Line, dec.Col,
		call.File, dec.File)
	if err != nil {
		sqliteErr := err.(*sqlite3.Error)
		if sqliteErr.Code() == sqlite3.CONSTRAINT_FOREIGNKEY {
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

func (db *WriterDB) getFileInfoBytesDB(file string) []byte {

	err := db.selectFileInfo.Query(file)
	switch {
	case err == io.EOF:
		return nil
	case err != nil:
		log.Panic("querying file info ", err)
	}
	defer db.selectFileInfo.Reset()

	var inDbFileInfo []byte
	err = db.selectFileInfo.Scan(&inDbFileInfo)
	if err != nil {
		log.Panic("scanning file info ", err)
	}

	return inDbFileInfo
}

func (db *WriterDB) InsertFile(file string, fiBytes []byte) {
	err := db.insertFile.Exec(file, fiBytes)
	if err != nil {
		log.Panic("insert file ", err)
	}

}

func (db *WriterDB) UptodateFile(file string) (bool, bool, []byte, error) {
	fi, err := os.Stat(file)
	if err != nil {
		log.Println(err, ": unable to read file ", file)
		return false, false, nil, err
	}

	fiBytes := getFileInfoBytes(fi)
	inDbFiBytes := db.getFileInfoBytesDB(file)

	exist := false
	uptodate := false
	if len(inDbFiBytes) > 0 {
		exist = true

		if bytes.Compare(fiBytes, inDbFiBytes) == 0 {
			// the file info in the DB and the file are the same;
			// nothing to process.
			uptodate = true
		}
	}

	return exist, uptodate, fiBytes, nil
}

func (db *WriterDB) RemoveFileReferences(file string) {
	err := db.delFileRef.Exec(file)
	if err != nil {
		log.Panic("delete file ", err)
	}
}

func (db *WriterDB) RemoveFileDepsReferences(file string) []string {
	db.conn.Begin()
	defer db.conn.Commit()

	deps := []string{}

	err := db.selectFileDeps.Query(file)
	switch {
	case err == io.EOF:
		return deps
	case err != nil:
		log.Panic("querying file deps ", err)
	}
	defer db.selectFileDeps.Reset()

	for {
		var dep string

		err = db.selectFileDeps.Scan(&dep)
		if err != nil {
			log.Panic("scan dep ", err)
		}

		deps = append(deps, dep)

		if db.selectFileDeps.Next() == io.EOF {
			break
		}
	}

	err = db.delDepends.Exec(file)
	if err != nil {
		log.Panic("delete depends ", err)
	}

	return deps
}

func (db *WriterDB) InsertFuncDef(def *Symbol) {
	// insert function definition. Ignore if already exists.
	err := db.insertFuncDef.Exec(def.Name, def.Unisr, def.Line, def.Col,
		def.File)
	if err != nil {
		log.Panic("insert func def ", err)
	}
}

func (db *WriterDB) InsertFuncSymb(dec, def *Symbol) {
	db.InsertFuncDef(def)
	db.InsertSymbol(dec)

	// point this declaration to its definition
	err := db.insertFuncDecDef.Exec(
		dec.Line, dec.Col,
		def.Line, def.Col,
		dec.File, def.File)
	if err != nil {
		log.Panic("insert func dec to def ", err)
	}
}

func (db *WriterDB) InsertDependency(file, head string) {
	err := db.insertDepend.Exec(file, head)
	if err != nil {
		log.Panic("insert dependency ", err)
	}
}

func (db *WriterDB) Close() {
	db.selectFileInfo.Close()
	db.selectFileDeps.Close()

	db.insertFile.Close()
	db.insertSymb.Close()
	db.insertFuncDef.Close()
	db.insertFuncDecDef.Close()
	db.insertSymbUse.Close()
	db.insertFuncCall.Close()
	db.insertDepend.Close()

	db.delFileRef.Close()
	db.delDepends.Close()

	db.conn.Close()
}

type DBConnFactory struct {
	path   string
	dbPath string
	conn   *sqlite3.Conn
}

func (db *DBConnFactory) initDB() {
	initStmt := `
        CREATE TABLE IF NOT EXISTS files (
            id          INTEGER,
            file_info   BLOB,
            path        TEXT UNIQUE NOT NULL,
            PRIMARY     KEY(id)
        );
	CREATE TABLE IF NOT EXISTS files_deps (
	    id          INTEGER,
	    depend	INTEGER,
	    PRIMARY     KEY(id, depend)
	    FOREIGN KEY(id) REFERENCES files(id) ON DELETE CASCADE
	    FOREIGN KEY(depend) REFERENCES files(id) ON DELETE CASCADE
	);
        CREATE TABLE IF NOT EXISTS symbol_decls (
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
        CREATE TABLE IF NOT EXISTS func_decs_defs (
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
        CREATE TABLE IF NOT EXISTS symbol_uses (
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
	err := db.conn.Exec(initStmt)
	if err != nil {
		log.Panic("init db ", err)
	}
}

func copyDb(src *sqlite3.Conn, dst *sqlite3.Conn) {
	backup, err := src.Backup("main", dst, "main")
	if err != nil {
		return
	}
	defer backup.Close()

	backup.Step(-1)
}

func getConn(dbPath string) *sqlite3.Conn {
	conn, err := sqlite3.Open(dbPath)
	if err != nil {
		log.Panic("open db ", err)
	}
	conn.Exec(`PRAGMA foreign_keys = ON;`)

	return conn
}

func NewDBConnFactory(path string) *DBConnFactory {
	dbPath := "file::memory:?cache=shared"

	r := &DBConnFactory{path, dbPath, getConn(dbPath)}

	// init DB

	ddb := getConn(path)
	copyDb(ddb, r.conn)
	ddb.Close()

	r.initDB()

	return r
}

func (fac *DBConnFactory) Close() {
	ddb := getConn(fac.path)
	copyDb(fac.conn, ddb)
	ddb.Close()

	fac.conn.Close()
}

func (fac *DBConnFactory) NewReader() *ReaderDB {
	conn := getConn(fac.dbPath)
	return NewReaderDB(conn)
}

func (fac *DBConnFactory) NewWriter() *WriterDB {
	conn := getConn(fac.dbPath)
	return NewWriterDB(conn)
}
