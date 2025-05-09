// Copyright 2014 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package symbolizer

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/pprof/internal/plugin"
	"github.com/google/pprof/internal/proftest"
	"github.com/google/pprof/profile"
)

const filePath = "mapping"
const buildID = "build-id"

var testM = []*profile.Mapping{
	{
		ID:      1,
		Start:   0x1000,
		Limit:   0x5000,
		File:    filePath,
		BuildID: buildID,
	},
}

var testL = []*profile.Location{
	{
		ID:      1,
		Mapping: testM[0],
		Address: 1000,
	},
	{
		ID:      2,
		Mapping: testM[0],
		Address: 2000,
	},
	{
		ID:      3,
		Mapping: testM[0],
		Address: 3000,
	},
	{
		ID:      4,
		Mapping: testM[0],
		Address: 4000,
	},
	{
		ID:      5,
		Mapping: testM[0],
		Address: 5000,
	},
}

var testProfile = profile.Profile{
	DurationNanos: 10e9,
	SampleType: []*profile.ValueType{
		{Type: "cpu", Unit: "cycles"},
	},
	Sample: []*profile.Sample{
		{
			Location: []*profile.Location{testL[0]},
			Value:    []int64{1},
		},
		{
			Location: []*profile.Location{testL[1], testL[0]},
			Value:    []int64{10},
		},
		{
			Location: []*profile.Location{testL[2], testL[0]},
			Value:    []int64{100},
		},
		{
			Location: []*profile.Location{testL[3], testL[0]},
			Value:    []int64{1},
		},
		{
			Location: []*profile.Location{testL[4], testL[3], testL[0]},
			Value:    []int64{10000},
		},
	},
	Location:   testL,
	Mapping:    testM,
	PeriodType: &profile.ValueType{Type: "cpu", Unit: "milliseconds"},
	Period:     10,
}

func TestSymbolization(t *testing.T) {
	sSym := symbolzSymbolize
	lSym := localSymbolize
	defer func() {
		symbolzSymbolize = sSym
		localSymbolize = lSym
		demangleFunction = Demangle
	}()
	symbolzSymbolize = symbolzMock
	localSymbolize = localMock
	demangleFunction = demangleMock

	type testcase struct {
		mode        string
		wantComment string
	}

	s := Symbolizer{
		Obj: mockObjTool{},
		UI:  &proftest.TestUI{T: t},
	}
	for i, tc := range []testcase{
		{
			"local",
			"local=[]",
		},
		{
			"fastlocal",
			"local=[fast]",
		},
		{
			"remote",
			"symbolz=[]",
		},
		{
			"",
			"local=[]:symbolz=[]",
		},
		{
			"demangle=none",
			"demangle=[none]:force:local=[force]:symbolz=[force]",
		},
		{
			"remote:demangle=full",
			"demangle=[full]:force:symbolz=[force]",
		},
		{
			"local:demangle=templates",
			"demangle=[templates]:force:local=[force]",
		},
		{
			"force:remote",
			"force:symbolz=[force]",
		},
	} {
		prof := testProfile.Copy()
		if err := s.Symbolize(tc.mode, nil, prof); err != nil {
			t.Errorf("symbolize #%d: %v", i, err)
			continue
		}
		sort.Strings(prof.Comments)
		if got, want := strings.Join(prof.Comments, ":"), tc.wantComment; got != want {
			t.Errorf("%q: got %s, want %s", tc.mode, got, want)
			continue
		}
	}
}

func symbolzMock(p *profile.Profile, force bool, sources plugin.MappingSources, syms func(string, string) ([]byte, error), ui plugin.UI) error {
	var args []string
	if force {
		args = append(args, "force")
	}
	p.Comments = append(p.Comments, "symbolz=["+strings.Join(args, ",")+"]")
	return nil
}

func localMock(p *profile.Profile, fast, force bool, obj plugin.ObjTool, ui plugin.UI) error {
	var args []string
	if fast {
		args = append(args, "fast")
	}
	if force {
		args = append(args, "force")
	}
	p.Comments = append(p.Comments, "local=["+strings.Join(args, ",")+"]")
	return nil
}

func demangleMock(p *profile.Profile, force bool, mode string) {
	if force {
		p.Comments = append(p.Comments, "force")
	}
	if mode != "" {
		p.Comments = append(p.Comments, "demangle=["+mode+"]")
	}
}

func TestLocalSymbolization(t *testing.T) {
	prof := testProfile.Copy()

	if prof.HasFunctions() {
		t.Error("unexpected function names")
	}
	if prof.HasFileLines() {
		t.Error("unexpected filenames or line numbers")
	}

	b := mockObjTool{}
	if err := localSymbolize(prof, false, false, b, &proftest.TestUI{T: t}); err != nil {
		t.Fatalf("localSymbolize(): %v", err)
	}

	for _, loc := range prof.Location {
		if err := checkSymbolizedLocation(loc.Address, loc.Line); err != nil {
			t.Errorf("location %d: %v", loc.Address, err)
		}
	}
	if !prof.HasFunctions() {
		t.Error("missing function names")
	}
	if !prof.HasFileLines() {
		t.Error("missing filenames or line numbers")
	}
}

func TestLocalSymbolizationHandlesSpecialCases(t *testing.T) {
	for _, tc := range []struct {
		desc, file, buildID, allowOutputRx string
		wantNumOutputRegexMatches          int
	}{{
		desc:    "Unsymbolizable files are skipped",
		file:    "[some unsymbolizable file]",
		buildID: "",
	}, {
		desc:    "HTTP URL like paths are skipped",
		file:    "http://original-url-source-of-profile-fetch",
		buildID: "",
	}, {
		desc:                      "Non-existent files are ignored",
		file:                      "/does-not-exist",
		buildID:                   buildID,
		allowOutputRx:             "(?s)unknown or non-existent file|Some binary filenames not available.*Try setting PPROF_BINARY_PATH",
		wantNumOutputRegexMatches: 2,
	}, {
		desc:                      "Missing main binary is detected",
		file:                      "",
		buildID:                   buildID,
		allowOutputRx:             "Main binary filename not available",
		wantNumOutputRegexMatches: 1,
	}, {
		desc:                      "Different build ID is detected",
		file:                      filePath,
		buildID:                   "unexpected-build-id",
		allowOutputRx:             "build ID mismatch",
		wantNumOutputRegexMatches: 1,
	},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			prof := testProfile.Copy()
			prof.Mapping[0].File = tc.file
			prof.Mapping[0].BuildID = tc.buildID
			origProf := prof.Copy()

			if prof.HasFunctions() {
				t.Error("unexpected function names")
			}
			if prof.HasFileLines() {
				t.Error("unexpected filenames or line numbers")
			}

			b := mockObjTool{}
			ui := &proftest.TestUI{T: t, AllowRx: tc.allowOutputRx}
			if err := localSymbolize(prof, false, false, b, ui); err != nil {
				t.Fatalf("localSymbolize(): %v", err)
			}
			if ui.NumAllowRxMatches != tc.wantNumOutputRegexMatches {
				t.Errorf("localSymbolize(): got %d matches for %q UI regexp, want %d", ui.NumAllowRxMatches, tc.allowOutputRx, tc.wantNumOutputRegexMatches)
			}

			if diff, err := proftest.Diff([]byte(origProf.String()), []byte(prof.String())); err != nil {
				t.Fatalf("Failed to get diff: %v", err)
			} else if string(diff) != "" {
				t.Errorf("Profile changed unexpectedly, diff(want->got):\n%s", diff)
			}
		})
	}
}

func checkSymbolizedLocation(a uint64, got []profile.Line) error {
	want, ok := mockAddresses[a]
	if !ok {
		return fmt.Errorf("unexpected address")
	}
	if len(want) != len(got) {
		return fmt.Errorf("want len %d, got %d", len(want), len(got))
	}

	for i, w := range want {
		g := got[i]
		if g.Function.Name != w.Func {
			return fmt.Errorf("want function: %q, got %q", w.Func, g.Function.Name)
		}
		if g.Function.Filename != w.File {
			return fmt.Errorf("want filename: %q, got %q", w.File, g.Function.Filename)
		}
		if g.Line != int64(w.Line) {
			return fmt.Errorf("want lineno: %d, got %d", w.Line, g.Line)
		}
		if g.Column != int64(w.Column) {
			return fmt.Errorf("want columnno: %d, got %d", w.Column, g.Column)
		}
	}
	return nil
}

var mockAddresses = map[uint64][]plugin.Frame{
	1000: {frame("fun11", "file11.src", 10, 1)},
	2000: {frame("fun21", "file21.src", 20, 2), frame("fun22", "file22.src", 20, 2)},
	3000: {frame("fun31", "file31.src", 30, 3), frame("fun32", "file32.src", 30, 3), frame("fun33", "file33.src", 30, 3)},
	4000: {frame("fun41", "file41.src", 40, 4), frame("fun42", "file42.src", 40, 4), frame("fun43", "file43.src", 40, 4), frame("fun44", "file44.src", 40, 4)},
	5000: {frame("fun51", "file51.src", 50, 5), frame("fun52", "file52.src", 50, 5), frame("fun53", "file53.src", 50, 5), frame("fun54", "file54.src", 50, 5), frame("fun55", "file55.src", 50, 5)},
}

func frame(fname, file string, line int, column int) plugin.Frame {
	return plugin.Frame{
		Func:   fname,
		File:   file,
		Line:   line,
		Column: column}
}

func TestDemangleSingleFunction(t *testing.T) {
	// All tests with default mode.
	demanglerMode := ""
	options := demanglerModeToOptions(demanglerMode)

	cases := []struct {
		symbol string
		want   string
	}{
		{
			// Trivial C symbol.
			symbol: "printf",
			want:   "printf",
		},
		{
			// foo::bar(int)
			symbol: "_ZN3foo3barEi",
			want:   "foo::bar",
		},
		{
			// Already demangled.
			symbol: "foo::bar(int)",
			want:   "foo::bar",
		},
		{
			// int foo::baz<double>(double)
			symbol: "_ZN3foo3bazIdEEiT",
			want:   "foo::baz",
		},
		{
			// Already demangled.
			//
			// TODO: The demangled form of this is actually
			// 'int foo::baz<double>(double)', but our heuristic
			// can't strip the return type. Should it be able to?
			symbol: "foo::baz<double>(double)",
			want:   "foo::baz",
		},
		{
			// operator delete[](void*)
			symbol: "_ZdaPv",
			want:   "operator delete[]",
		},
		{
			// OSX prepends extra '_', which we're not able to remove. But we handle it when demangling.
			symbol: "__ZdaPv",
			want:   "operator delete[]",
		},
		{
			// Leave special double underscore symbols as is.
			symbol: "__some_special_name",
			want:   "__some_special_name",
		},
		{
			// Already demangled.
			symbol: "operator delete[](void*)",
			want:   "operator delete[]",
		},
		{
			// bar(int (*) [5])
			symbol: "_Z3barPA5_i",
			want:   "bar",
		},
		{
			// Already demangled.
			symbol: "bar(int (*) [5])",
			want:   "bar",
		},
		// Java symbols, do not demangle.
		{
			symbol: "java.lang.Float.parseFloat",
			want:   "java.lang.Float.parseFloat",
		},
		{
			symbol: "java.lang.Float.<init>",
			want:   "java.lang.Float.<init>",
		},
		// Go symbols, do not demangle.
		{
			symbol: "example.com/foo.Bar",
			want:   "example.com/foo.Bar",
		},
		{
			symbol: "example.com/foo.(*Bar).Bat",
			want:   "example.com/foo.(*Bar).Bat",
		},
		{
			// Method on type with type parameters, as reported by
			// Go pprof profiles (simplified symbol name).
			symbol: "example.com/foo.(*Bar[...]).Bat",
			want:   "example.com/foo.(*Bar[...]).Bat",
		},
		{
			// Method on type with type parameters, as reported by
			// perf profiles (actual symbol name).
			symbol: "example.com/foo.(*Bar[go.shape.string_0,go.shape.int_1]).Bat",
			want:   "example.com/foo.(*Bar[go.shape.string_0,go.shape.int_1]).Bat",
		},
		{
			// Function with type parameters, as reported by Go
			// pprof profiles (simplified symbol name).
			symbol: "example.com/foo.Bar[...]",
			want:   "example.com/foo.Bar[...]",
		},
		{
			// Function with type parameters, as reported by perf
			// profiles (actual symbol name).
			symbol: "example.com/foo.Bar[go.shape.string_0,go.shape.int_1]",
			want:   "example.com/foo.Bar[go.shape.string_0,go.shape.int_1]",
		},
	}
	for _, tc := range cases {
		fn := &profile.Function{
			SystemName: tc.symbol,
		}
		demangleSingleFunction(fn, options)
		if fn.Name != tc.want {
			t.Errorf("demangleSingleFunction(%s) got %s want %s", tc.symbol, fn.Name, tc.want)
		}
	}
}

type mockObjTool struct{}

func (mockObjTool) Open(file string, start, limit, offset uint64, relocationSymbol string) (plugin.ObjFile, error) {
	if file != filePath {
		return nil, fmt.Errorf("unknown or non-existent file %q", file)
	}
	return mockObjFile{frames: mockAddresses}, nil
}

func (mockObjTool) Disasm(file string, start, end uint64, intelSyntax bool) ([]plugin.Inst, error) {
	if file != filePath {
		return nil, fmt.Errorf("unknown or non-existent file %q", file)
	}
	return nil, fmt.Errorf("disassembly not supported")
}

type mockObjFile struct {
	frames map[uint64][]plugin.Frame
}

func (mockObjFile) Name() string {
	return filePath
}

func (mockObjFile) ObjAddr(addr uint64) (uint64, error) {
	return addr, nil
}

func (mockObjFile) BuildID() string {
	return buildID
}

func (mf mockObjFile) SourceLine(addr uint64) ([]plugin.Frame, error) {
	return mf.frames[addr], nil
}

func (mockObjFile) Symbols(r *regexp.Regexp, addr uint64) ([]*plugin.Sym, error) {
	return []*plugin.Sym{}, nil
}

func (mockObjFile) Close() error {
	return nil
}
