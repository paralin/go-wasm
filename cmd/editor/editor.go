package main

import (
	"io/ioutil"
	"syscall/js"

	"github.com/johnstarich/go-wasm/cmd/editor/ide"
	"github.com/johnstarich/go-wasm/log"
)

// editorJSFunc is a JS function that opens on a JS element and returns a JS object with the following spec:
// {
//   getContents() string
//   setContents(string)
//   getCursorIndex() int
//   setCursorIndex(int)
// }
type editorJSFunc js.Value

func (e editorJSFunc) New(elem js.Value) ide.Editor {
	editor := &jsEditor{
		titleChan: make(chan string, 1),
	}
	editor.elem = js.Value(e).Invoke(elem, js.FuncOf(editor.onEdit))
	return editor
}

type jsEditor struct {
	elem      js.Value
	filePath  string
	titleChan chan string
}

func (j *jsEditor) onEdit(js.Value, []js.Value) interface{} {
	contents := j.elem.Call("getContents").String()
	err := ioutil.WriteFile(j.filePath, []byte(contents), 0700)
	if err != nil {
		log.Error("Failed to write file contents: ", err)
	}
	return nil
}

func (j *jsEditor) OpenFile(path string) error {
	j.filePath = path
	j.titleChan <- path
	return j.ReloadFile()
}

func (j *jsEditor) CurrentFile() string {
	return j.filePath
}

func (j *jsEditor) ReloadFile() error {
	contents, err := ioutil.ReadFile(j.filePath)
	if err != nil {
		return err
	}
	j.elem.Call("setContents", string(contents))
	return nil
}

func (j *jsEditor) GetCursor() int {
	return j.elem.Call("getCursorIndex").Int()
}

func (j *jsEditor) SetCursor(i int) error {
	j.elem.Call("setCursorIndex", i)
	return nil
}

func (j *jsEditor) Titles() <-chan string {
	return j.titleChan
}
