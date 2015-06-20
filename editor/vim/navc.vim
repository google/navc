" Copyright 2015 Google Inc. All Rights Reserved.
"
" Licensed under the Apache License, Version 2.0 (the "License");
" you may not use this file except in compliance with the License.
" You may obtain a copy of the License at
"
"    http://www.apache.org/licenses/LICENSE-2.0
"
" Unless required by applicable law or agreed to in writing, software
" distributed under the License is distributed on an "AS IS" BASIS,
" WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
" See the License for the specific language governing permissions and
" limitations under the License.

" Very simple plugin that takes the symbol in the current cursor, asks navc
" daemon for its declaration, and point the cursor in the declaration. For
" this to run properly, the PYTHONPATH has to be set. From the root of the
" project directory:
" 	PYTHONPATH=third_party/jsonrpc/ vim
"
" To use, simply source this file in vim, put the cursor on top of the symbol
" of interest, and call the function FindCursorSymbolDecl(). In vim
" 	:source editor/vim/navc.vim
" 	(move cursor to symbol)
" 	:call FindCursorSymbolDecl()

if !has('python')
	echo "Error: Required vim compiled with +python"
	finish
endif

python << EOF
import vim
import re
import jsonrpc
import os
import sys

fname_char = re.compile('[a-zA-Z_]')

def find_start_cur_symbol():
	row, col = vim.current.window.cursor
	while fname_char.match(vim.current.buffer[row - 1][col - 1]):
		col -= 1
	return (row, col + 1)

def get_choice():
	vim.command("call inputsave()")
	vim.command("let choice = input('Input Choice: ')")
	vim.command("call inputrestore()")
	return vim.eval("choice")

def get_choice_int():
	# TODO: we need to make sure that a number was given and that it is
	# within boundaries.
	ch = get_choice()
	return int(ch)

def conn():
	return jsonrpc.ServerProxy(jsonrpc.JsonRpc10(),
			jsonrpc.TransportUnixSocket(addr='/tmp/navc.sock'))

def get_cursor_input():
	line, col = find_start_cur_symbol()
	fname = os.path.relpath(vim.current.buffer.name)

	args = {
		"File": fname,
		"Line": line,
		"Col": col,
	}

	return args

def move_cursor(fname, line, col):
	vim.command('edit %s' % fname)
	vim.current.window.cursor = (line, col)
EOF

" This function will find the declaration of the symbol currently under the
" cursor.
function! FindCursorSymbolDecl()
python << EOF

try:
	ret = conn().RequestHandler.GetSymbolDecl(get_cursor_input())
	move_cursor(ret['File'], ret['Line'], ret['Col'] - 1)
except jsonrpc.RPCFault as e:
	print >> sys.stderr, e.error_data

EOF
endfunction

" This function will find all the uses of symbol declaration. This should work
" well to find all symbols use of a declaration in a header file.
function! FindCursorSymbolUses()
python << EOF
try:
	ret = conn().RequestHandler.GetSymbolUses(get_cursor_input())
	num = 1
	for op in ret:
		print '(%2d) %s %d' % (num, op['File'], op['Line'])
		num += 1
	ch = get_choice_int()
	ch -= 1
	move_cursor(ret[ch]['File'], ret[ch]['Line'], ret[ch]['Col'] - 1)
except jsonrpc.RPCFault as e:
	print >> sys.stderr, e.error_data
EOF
endfunction
