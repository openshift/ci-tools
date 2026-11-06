/*
Copyright © 2020 Corey Daley <cdaley@redhat.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package imports

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	klog "k8s.io/klog/v2"
)

type ImportRegexp struct {
	Bucket string
	Regexp *regexp.Regexp
}

type byPathValue []ast.ImportSpec

func (a byPathValue) Len() int           { return len(a) }
func (a byPathValue) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byPathValue) Less(i, j int) bool { return a[i].Path.Value < a[j].Path.Value }

var (
	impLine      = regexp.MustCompile(`^\s+(?:[\w\.]+\s+)?"(.+)"`)
	vendor       = regexp.MustCompile(`vendor/`)
	importRegexp []ImportRegexp
	importOrder  = []string{
		"standard",
		"other",
		"kubernetes",
		"openshift",
		"module",
	}
)

// taken from https://github.com/golang/tools/blob/71482053b885ea3938876d1306ad8a1e4037f367/internal/imports/imports.go#L380
func addSpaces(r io.Reader, breaks []string) ([]byte, error) {
	var out bytes.Buffer
	in := bufio.NewReader(r)
	inImports := false
	done := false
	for {
		s, err := in.ReadString('\n')
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		if !inImports && !done && strings.HasPrefix(s, "import") {
			inImports = true
		}
		if inImports && (strings.HasPrefix(s, "var") ||
			strings.HasPrefix(s, "func") ||
			strings.HasPrefix(s, "const") ||
			strings.HasPrefix(s, "type")) {
			done = true
			inImports = false
		}
		if inImports && len(breaks) > 0 {
			if m := impLine.FindStringSubmatch(s); m != nil {
				if m[1] == breaks[0] {
					out.WriteByte('\n')
					breaks = breaks[1:]
				}
			}
		}

		fmt.Fprint(&out, s)
	}
	return out.Bytes(), nil
}

// Format takes a channel of file paths and formats the files imports
func Format(files chan string, wg *sync.WaitGroup, modulePtr *string, dry *bool) {
	defer wg.Done()
	importRegexp = []ImportRegexp{
		{Bucket: "module", Regexp: regexp.MustCompile(*modulePtr)},
		{Bucket: "kubernetes", Regexp: regexp.MustCompile("k8s.io")},
		{Bucket: "openshift", Regexp: regexp.MustCompile("github.com/openshift")},
		{Bucket: "other", Regexp: regexp.MustCompile("[a-zA-Z0-9]+\\.[a-zA-Z0-9]+/")},
	}

	for path := range files {
		if len(path) == 0 {
			continue
		}
		klog.V(2).Infof("Processing %s", path)
		importGroups := map[string][]ast.ImportSpec{
			"standard":   {},
			"other":      {},
			"kubernetes": {},
			"openshift":  {},
			"module":     {},
		}
		var breaks []string
		fs := token.NewFileSet()
		contents, err := ioutil.ReadFile(path)
		if err != nil {
			klog.Errorf("%#v", err)
		}
		f, err := parser.ParseFile(fs, "", contents, parser.ParseComments)
		if err != nil {
			klog.Fatalf("%#v", err)
		}

		for _, i := range f.Imports {
			if len(i.Path.Value) == 0 {
				continue
			}
			found := false
			for _, r := range importRegexp {
				if r.Regexp.MatchString(i.Path.Value) {
					importGroups[r.Bucket] = append(importGroups[r.Bucket], *i)
					found = true
					break
				}
			}
			if !found {
				importGroups["standard"] = append(importGroups["standard"], *i)
			}
		}

		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if ok && gen.Tok == token.IMPORT {
				gen.Specs = []ast.Spec{}
				for _, group := range importOrder {
					sort.Sort(byPathValue(importGroups[group]))
					for n := range importGroups[group] {
						importGroups[group][n].EndPos = 0
						importGroups[group][n].Path.ValuePos = 0
						if importGroups[group][n].Name != nil {
							importGroups[group][n].Name.NamePos = 0
						}
						gen.Specs = append(gen.Specs, &importGroups[group][n])
						if n == 0 && group != importOrder[0] {
							newstr, err := strconv.Unquote(importGroups[group][n].Path.Value)
							if err != nil {
								klog.Errorf("%#v", err)
							}
							breaks = append(breaks, newstr)
						}
					}
				}
			}
		}

		printerMode := printer.TabIndent

		printConfig := &printer.Config{Mode: printerMode, Tabwidth: 4}

		var buf bytes.Buffer
		if err = printConfig.Fprint(&buf, fs, f); err != nil {
			klog.Errorf("%#v", err)
		}
		out, err := addSpaces(bytes.NewReader(buf.Bytes()), breaks)
		out, err = format.Source(out)
		if bytes.Compare(contents, out) != 0 {
			if *dry {
				klog.Infof("%s is not sorted", path)
			} else {
				info, err := os.Stat(path)
				if err = ioutil.WriteFile(path, out, info.Mode()); err != nil {
					klog.Errorf("%#v", err)
				}
				klog.Infof("%s updated", path)
			}
		}

	}
}
