// تست نگهبان AST (مأموریت ۶ — ایراد ۵): هر exec.Command/exec.CommandContext
// در کد غیرتستی باید پنجره‌ی کنسولش در ویندوز مخفی شود، وگرنه هنگام ارسال هر
// پیام (چک‌پوینت git، MCP، …) یک پنجره‌ی سیاه لحظه‌ای فلش می‌زند (رفع‌شده در
// 1.10.0). پوشش معتبر: فراخوانی hideConsoleWindow(var) یا configureShell(var,…)
// در همان تابع، یا تخصیص مستقیم SysProcAttr روی var (الگوی taskkill در
// shell_windows.go). استثنا فقط با دلیل در consoleHideAllowList.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// consoleHideAllowList: "file.go:var" → دلیل. فقط برای سایتی که پوشش کنسول
// بی‌معناست (مثلاً شاخه‌ی POSIX) — هر ورودی تازه باید توجیه شود.
var consoleHideAllowList = map[string]string{}

func TestEveryExecCommandHidesConsole(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	type site struct {
		file, fn, varName string
	}
	var uncovered []string
	siteCount := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, perr := parser.ParseFile(fset, f, nil, 0)
		if perr != nil {
			t.Fatalf("%s: parse: %v", f, perr)
		}
		for _, decl := range af.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			// نام تابع برای گزارش/allow-list
			fnName := "??"
			if fd.Recv != nil {
				if rt, ok := fd.Recv.List[0].Type.(*ast.StarExpr); ok {
					if id, ok := rt.X.(*ast.Ident); ok {
						fnName = id.Name + "." + fd.Name.Name
					}
				}
			} else {
				fnName = fd.Name.Name
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || len(as.Rhs) != 1 {
					return true
				}
				call, ok := as.Rhs[0].(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "exec" {
					return true
				}
				if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
					return true
				}
				siteCount++
				varName := ""
				if len(as.Lhs) == 1 {
					if id, ok := as.Lhs[0].(*ast.Ident); ok {
						varName = id.Name
					}
				}
				if varName == "" {
					uncovered = append(uncovered, f+":"+fnName+" (lhs not a plain ident)")
					return true
				}
				if coveredByConsoleHide(fd, varName) {
					return true
				}
				key := f + ":" + varName
				if _, allowed := consoleHideAllowList[key]; allowed {
					return true
				}
				uncovered = append(uncovered, f+":"+fnName+" var="+varName)
				return true
			})
		}
	}
	if siteCount == 0 {
		t.Fatal("no exec.Command sites found — the AST guard lost its target (refactor?)")
	}
	if len(uncovered) > 0 {
		sort.Strings(uncovered)
		t.Fatalf("exec.Command sites without console hiding (flash black window on Windows):\n  %s\n"+
			"Fix: call hideConsoleWindow(cmd) right after creating the command,\n"+
			"or use configureShell(cmd, command) — or justify an entry in consoleHideAllowList.",
			strings.Join(uncovered, "\n  "))
	}
}

// coveredByConsoleHide آیا در بدنه‌ی همان تابع، var پوشش می‌شود؟
func coveredByConsoleHide(fd *ast.FuncDecl, varName string) bool {
	covered := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok {
				if (id.Name == "hideConsoleWindow" || id.Name == "configureShell") && len(x.Args) > 0 {
					if a, ok := x.Args[0].(*ast.Ident); ok && a.Name == varName {
						covered = true
					}
				}
			}
		case *ast.AssignStmt:
			// cmd.SysProcAttr = … (تخصیص مستقیم، الگوی taskkill)
			for i, lhs := range x.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "SysProcAttr" {
					continue
				}
				if b, ok := sel.X.(*ast.Ident); ok && b.Name == varName && i < len(x.Rhs) {
					covered = true
				}
			}
		}
		return true
	})
	return covered
}

// TestHideConsoleHelperExists هر دو نسخه‌ی build-tagged helper را بررسی می‌کند.
func TestHideConsoleHelperExists(t *testing.T) {
	for _, f := range []string{"console_windows.go", "console_other.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s missing: %v", f, err)
		}
		if !strings.Contains(string(b), "func hideConsoleWindow(cmd *exec.Cmd)") {
			t.Fatalf("%s must define hideConsoleWindow(cmd *exec.Cmd)", f)
		}
	}
	wb, err := os.ReadFile("console_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(wb)
	for _, want := range []string{"HideWindow:    true", "0x08000000"} {
		if !strings.Contains(s, want) {
			t.Fatalf("console_windows.go must contain %q (CREATE_NO_WINDOW contract)", want)
		}
	}
}
