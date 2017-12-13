package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"regexp"
	"text/template"
	"strconv"
	"strings"
	"log"
	"bufio"
)

var (
	importName = "__errgotrace"

	importStmt = `
/* BEGIN_ERRGOTRACE */
import __errgotrace "github.com/gellweiler/errgotrace/log"
/* END_ERRGOTRACE */
`
	setup = `
/* BEGIN_ERRGOTRACE */
var _ = __errgotrace.Setup()
/* END_ERRGOTRACE */
`

	tmpl = `
/* BEGIN_ERRGOTRACE */
	{{.resultvars}} := {{if .callreceiver}}{{.callreceiver}}.{{end}}__{{.fname}}({{.callparams}})
	__errgotrace.InspectReturnValues("{{.outputfname}}", {{.resultvars}})
	return {{.resultvars}}
}

func {{.receiver}}__{{.fname}}{{.params}}{{.returns}} {
	/* END_ERRGOTRACE */
`
	cmdMessagePrefix =
`Errgotrace modifies go files to include code for tracing go errors.

usage: errgotrace [flags] [path ...]
`

	cmdMessageSuffix = `
Examples:
  Add tracing code to all go files in the current directory.
  $ find . -name '*.go' -print0 | xargs -0 errgotrace -w

  Add tracing code to all go files in the current directory.
  Exclude vendor dir.
  $ find . -path ./vendor -prune -o -name '*.go' -print0 | xargs -0 errgotrace -w

  Remove all tracing code from all go files in the current directory.
  $ find . -path ./vendor -prune -o -name '*.go' -print0 | xargs -0 errgotrace -w -r
`

	beginRegex = regexp.MustCompile("^\\s*/\\* BEGIN_ERRGOTRACE \\*/\\s*")

	endRegex = regexp.MustCompile("^\\s*/\\* END_ERRGOTRACE \\*/\\s*")
)

var (
	fset         *token.FileSet
	funcTemplate *template.Template
	exportedOnly bool
	writeFiles   bool
	reverseProcess   bool
	filterFlag   string
	excludeFlag  string
	formatLength int
	timing       bool

	filter  *regexp.Regexp
	exclude *regexp.Regexp
)

// convert function parameters to a list of names
func paramNames(params *ast.FieldList) []string {
	var p []string
	for _, f := range params.List {
		for _, n := range f.Names {
			// we can't use _ as a name, so ignore it
			if n.Name != "_" {
				p = append(p, n.Name)
			}
		}
	}
	return p
}

// Generate the debug code for a function. Will get injected just below the function def.
func generateDebugCode(funcName string, f *ast.FuncDecl, orig []byte) ([]byte) {
	vals := make(map[string]string)
	vals["outputfname"] = funcName

	// Don't alter functions that have no return values.
	if f.Type.Results == nil || len(f.Type.Results.List) < 1 {
		return []byte("")
	}

	vals["fname"] = f.Name.String()

	// Get the list with return values
	vals["returns"] = string(orig[f.Type.Results.Pos()-1:f.Type.Results.End()])

	// Get the list with named parameters, skip unnamed parameters.
	vals["params"] = ""
	sep := ""
	for _, p := range f.Type.Params.List {
		for _, n := range p.Names {
			if n.Name == "_" {
				continue
			}

			name := string(orig[n.Pos()-1:n.End()-1])
			t := string(orig[p.Type.Pos()-1:p.Type.End()-1])
			vals["params"] += sep + name +  " " + t
			sep = ", "
		}
	}
	vals["params"] = "(" + vals["params"] + ")"

	// Get the function receiver if any
	vals["receiver"] = ""
	if f.Recv != nil && len(f.Recv.List) > 0 {
		// For unnamed receivers do not use the receiver in the backend function
		// but instead prepend the name of the receiver tpye to the function
		if len(f.Recv.List[0].Names) < 1 || f.Recv.List[0].Names[0].Name == "_" {
			t := string(orig[f.Recv.List[0].Type.Pos()-1:f.Recv.List[0].Type.End()-1])
			t = strings.Replace(t, "*", "__", -1)
			t = strings.Replace(t, "*", "_s", -1)
			t = strings.Replace(t, "[", "_o", -1)
			t = strings.Replace(t, "]", "_c", -1)
			vals["fname"] = t + "_" + vals["fname"]
		} else {
			vals["receiver"] = string(orig[f.Recv.Pos()-1:f.Recv.End()-1])
		}
	}

	// Generate a set of variables that can hold the result of the function call
	vals["resultvars"] = ""
	sep = ""
	i := 0
	for _, field := range f.Type.Results.List {
		for j := 0; j == 0 || (field.Names != nil && j < len(field.Names)); j++ {
			vals["resultvars"] += sep + "__result" + strconv.Itoa(i)
			sep = ", "
			i++
		}
	}

	// Generate the receiver for the function call to use, if any
	if f.Recv != nil && len(f.Recv.List) > 0 {
		for _, r := range f.Recv.List {
			// Ignore unanmed receivers.
			if len(r.Names) > 0 && r.Names[0].Name != "_" {
				vals["callreceiver"] = r.Names[0].Name
			}
		}
	}

	// Generate the paramaters for the function call
	vals["callparams"] = ""
	sep = ""
	for _, field := range f.Type.Params.List { // function params
		for _, name := range field.Names {
			if name.Name == "_" { // skip unnamed parameters
				continue
			}


			vals["callparams"] += sep  + name.Name

			// If this is a variadic paramter, append ...
			if string(orig[field.Type.Pos()-1:field.Type.Pos()+2]) == "..." {
				vals["callparams"] += "..."
			}

			sep = ", "
		}
	}

	var enterBuffer bytes.Buffer
	err := funcTemplate.Execute(&enterBuffer, vals)
	if err != nil {
		log.Fatal(err)
	}

	return enterBuffer.Bytes()
}

type edit struct {
	pos int
	val []byte
}

type editList struct {
	edits       []edit
	packageName string
	orig 		[]byte
}

func (e *editList) Add(pos int, val []byte) {
	e.edits = append(e.edits, edit{pos: pos, val: val})
}

// Check if given ast node is a function, if so generate the debug code for it.
func (e *editList) inspect(node ast.Node) bool {
	if node == nil {
		return false
	}

	// Check if given node is a function
	var f *ast.FuncDecl
	var ok bool
	if f, ok = node.(*ast.FuncDecl); !ok {
		return true
	}

	// Skip functions without a body
	if f.Body == nil {
		return true
	}

	// function name = package + receiverType + function ident
	funcName := e.packageName
	if f.Recv != nil && len(f.Recv.List) > 0 {
		funcName += "." + string(e.orig[f.Recv.List[0].Type.Pos()-1:f.Recv.List[0].Type.End()-1])
	}
	funcName += "." + f.Name.Name

	// Skip functions, if they don't match the given filter
	if !filter.MatchString(funcName) {
		return true
	}

	// Skip functions, if they match the given filter
	if exclude != nil && exclude.MatchString(funcName) {
		return true
	}

	if exportedOnly && !ast.IsExported(funcName) {
		return true
	}

	injection := generateDebugCode(funcName, f, e.orig)
	e.Add(int(f.Body.Lbrace), injection)

	return true
}

// process file
func annotateFile(file string) error {
	orig, err := ioutil.ReadFile(file)
	if err != nil {
		return fmt.Errorf("%s: failed to open (%s)", file, err)
	}

	src, err := annotate(file, orig)
	if err != nil {
		return err
	}

	if !writeFiles {
		fmt.Println(string(src))
	} else {
		err = ioutil.WriteFile(file, src, 0)
		if err != nil {
			return fmt.Errorf("%s: failed to write (%s)", file, err)
		}
	}

	return nil
}

// process the contents of a go file
func annotate(filename string, orig []byte) ([]byte, error) {
	// we need to make sure the source is formatted to insert the new code in the expected place
	orig, err := format.Source(orig)
	if err != nil {
		return nil, fmt.Errorf("%s: formatting error (%s)", filename, err.Error())
	}

	fset = token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, orig, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	for _, imp := range f.Imports {
		if imp.Name != nil && imp.Name.Name == importName {
			return nil, fmt.Errorf("%s: already processed", filename)
		}
	}

	edits := editList{packageName: f.Name.Name, orig : orig}

	// insert our import directly after the package line
	edits.Add(int(f.Name.End()), []byte(importStmt))

	ast.Inspect(f, edits.inspect)

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, fmt.Errorf("%s: format.Node (%s)", filename, err.Error())
	}

	data := buf.Bytes()

	var pos int
	var out []byte
	for _, e := range edits.edits {
		out = append(out, data[pos:e.pos]...)
		out = append(out, []byte(e.val)...)
		pos = e.pos
	}
	out = append(out, data[pos:]...)

	// it's easier to append the setup code at the end
	out = append(out, []byte(setup)...)

	src, err := format.Source(out)
	if err != nil {
		return nil, fmt.Errorf("%s: formatting error (%s)", filename, err.Error())
	}

	return src, nil
}

func init() {
	funcTemplate = template.Must(template.New("debug").Parse(tmpl))
}

// Remove tracking code from file
func reverseFile(filename string) error {
	type tState int
	const (
		NORMAL tState = iota
		NORMAL_ENTER
		ERRGOTRACE
	)

	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("%s: failed to open (%s)", filename, err)
	}

	var state tState = NORMAL

	out := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if state == NORMAL_ENTER {
			if line != "" {
				state = NORMAL
			}
		}

		if state == NORMAL {
			if beginRegex.MatchString(line) {
				state = ERRGOTRACE
			} else {
				out += line + "\n"
			}
		}

		if state == ERRGOTRACE {
			if endRegex.MatchString(line) {
				state = NORMAL_ENTER
			}
		}
	}

	out = strings.TrimRight(out, "\n")

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%s: failed to read (%s)", filename, err)
	}

	if !writeFiles {
		fmt.Print(out)
	} else {
		err = ioutil.WriteFile(filename, []byte(out), 0)
		if err != nil {
			return fmt.Errorf("%s: failed to write (%s)", filename, err)
		}
	}

	return nil
}

func main() {
	flag.BoolVar(&exportedOnly, "exported", false, "only annotate exported functions")
	flag.BoolVar(&writeFiles, "w", false, "re-write files in place")
	flag.StringVar(&filterFlag, "filter", ".", "only annotate functions matching the regular expression")
	flag.StringVar(&excludeFlag, "exclude", "", "exclude any matching functions, takes precedence over filter")
	flag.BoolVar(&reverseProcess, "r", false, "reverse the process, remove tracing code")
	flag.Parse()

	if flag.NArg() < 1 {
		os.Stdout.Write([]byte(cmdMessagePrefix))
		flag.PrintDefaults()
		os.Stdout.Write([]byte(cmdMessageSuffix))
		os.Exit(1)
	}

	var err error
	filter, err = regexp.Compile(filterFlag)
	if err != nil {
		log.Printf("error in filter regex (%s)", err.Error())
		os.Exit(1)
	}

	if excludeFlag != "" {
		exclude, err = regexp.Compile(excludeFlag)
		if err != nil {
			log.Printf("error in exclude regex (%s)", err.Error())
			os.Exit(1)
		}
	}

	var failure bool = false
	for _, file := range flag.Args() {
		var err error
		if reverseProcess {
			err = reverseFile(file)
		} else {
			err = annotateFile(file)
		}
		if err != nil {
			log.Print(err)
			failure = true
		}
	}

	if (failure) {
		os.Exit(1)
	}
}
