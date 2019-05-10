package sentry

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/pkg/errors"
)

type stackTracer interface {
	StackTrace() errors.StackTrace
}

type stackTraceFrames []*stackTraceFrame

func (c stackTraceFrames) Len() int      { return len(c) }
func (c stackTraceFrames) Swap(i, j int) { c[j], c[i] = c[i], c[j] }
func (c stackTraceFrames) Reverse() {
	for i, j := 0, c.Len()-1; i < j; i, j = i+1, j-1 {
		c.Swap(i, j)
	}
}

func getStacktraceFramesForError(err error) stackTraceFrames {
	if err, ok := err.(stackTracer); ok {
		frames := stackTraceFrames{}
		for _, f := range err.StackTrace() {
			pc := uintptr(f) - 1
			frame := getStacktraceFrame(pc)
			if frame != nil {
				frames = append(frames, frame)
			}
		}

		frames.Reverse()
		return frames
	}

	return stackTraceFrames{}
}

func getStacktraceFrames(skip int) stackTraceFrames {
	pcs := make([]uintptr, 30)
	if c := runtime.Callers(skip+2, pcs); c > 0 {
		frames := stackTraceFrames{}
		for _, pc := range pcs {
			frame := getStacktraceFrame(pc)
			if frame != nil {
				frames = append(frames, frame)
			}
		}

		frames.Reverse()
		return frames
	}

	return stackTraceFrames{}
}

func getStacktraceFrame(pc uintptr) *stackTraceFrame {
	frame := &stackTraceFrame{}

	if fn := runtime.FuncForPC(pc); fn != nil {
		frame.AbsoluteFilename, frame.Line = fn.FileLine(pc)
		frame.Package, frame.Module, frame.Function = formatFuncName(fn.Name())
		frame.Filename = shortFilename(frame.AbsoluteFilename, frame.Package)
	} else {
		frame.AbsoluteFilename = "unknown"
		frame.Filename = "unknown"
	}

	return frame
}

func (f *stackTraceFrame) ClassifyInternal(internalPrefixes []string) {
	if f.Module == "main" {
		f.InApp = true
		return
	}

	for _, prefix := range internalPrefixes {
		if strings.HasPrefix(f.Package, prefix) && !strings.Contains(f.Package, "vendor") {
			f.InApp = true
			return
		}
	}
}

func formatFuncName(fnName string) (pack, module, name string) {
	name = fnName
	pack = ""
	module = ""

	if idx := strings.LastIndex(name, "/"); idx != -1 {
		n := name[idx+1:]
		if idx2 := strings.Index(n, "."); idx2 != -1 {
			pack = name[:idx+idx2+1]
			module = n[0:idx2]
			name = n[idx2+1:]
		}
	} else if idx := strings.Index(name, "."); idx != -1 {
		pack = name[0:idx]
		module = pack
		name = name[idx+1:]
	}

	name = strings.Replace(name, "·", ".", -1)

	return
}

func shortFilename(absFile, pkg string) string {
	if pkg == "" {
		return absFile
	}

	if idx := strings.Index(absFile, fmt.Sprintf("%s/", pkg)); idx != -1 {
		return absFile[idx:]
	}

	return absFile
}
