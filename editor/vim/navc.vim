" Copyright 2016 Google Inc. All Rights Reserved.
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

if !has('python')
	echo "Error: Required vim compiled with +python"
	finish
endif

python << EOF
import os
import sys
sys.path.append(os.path.expanduser('~/.vim/plugin/navc/'))
import navc_vim as navc
EOF

" This function will find the declaration of the symbol currently under the
" cursor.
function! FindCursorSymbolDecls()
python navc.find_cursor_decl()
endfunction

" This function will find all the uses of symbol declaration. This should work
" well to find all symbols use of a declaration in a header file.
function! FindCursorSymbolUses()
python navc.find_cursor_uses()
endfunction

function! FindCursorSymbolDef()
python navc.find_symbol_def()
endfunction

function! MoveCursorToPrev()
python navc.move_cursor_to_prev()
endfunction

nnoremap <C-z>e :call FindCursorSymbolDecls()<ENTER>
nnoremap <C-z>b :call MoveCursorToPrev()<ENTER>
nnoremap <C-z>u :call FindCursorSymbolUses()<ENTER>
nnoremap <C-z>d :call FindCursorSymbolDef()<ENTER>
