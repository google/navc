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
EOF

function! FindCursorSymbolDecl()
python << EOF
server = jsonrpc.ServerProxy(jsonrpc.JsonRpc10(),
jsonrpc.TransportUnixSocket(addr='/tmp/navc.sock'))

line, col = find_start_cur_symbol()
fname = os.path.relpath(vim.current.buffer.name)
#print fname, line, col

args = {
	"File": fname,
	"Line": line,
	"Col": col,
}
try:
	ret = server.RequestHandler.GetSymbolDecl(args)
	#print ret
	vim.command('edit %s' % ret['File'])
	vim.current.window.cursor = (ret['Line'], ret['Col'] - 1)
except jsonrpc.RPCFault as e:
	print >> sys.stderr, e.error_data

EOF
endfunction
