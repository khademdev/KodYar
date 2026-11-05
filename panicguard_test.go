// تست ساختاری (AST): هر متد صادرشده‌ی *App — یعنی هر متدی که Wails به
// فرانت‌اند متصل می‌کند — باید در همان ابتدای بدنه محافظ panic داشته باشد
// (defer a.recoverBound یا defer a.recoverBoundErr). یک panic مهارنشده در
// متد متصل، کل برنامه (و کار ذخیره‌نشده‌ی کاربر) را از بین می‌برد.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// boundGuardAllowList استثناهای موجه: متدهای صادرشده‌ای که محافظ لازم ندارند.
// فعلاً خالی است — حتی getterهای ساده هم پوشش دارند. هر ورودی باید دلیل
// روشن داشته باشد (نه «کار مهمی نمی‌کند»).
var boundGuardAllowList = map[string]string{}

// receiverIsApp آیا گیرنده‌ی متد *App است؟
func receiverIsApp(fd *ast.FuncDecl) bool {
	if fd.Recv == nil || len(fd.Recv.List) != 1 {
		return false
	}
	star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "App"
}

// bodyHasRecoverBoundDefer آیا بدنه یک defer با فراخوانی a.recoverBound یا
// a.recoverBoundErr دارد؟
func bodyHasRecoverBoundDefer(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		ds, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		sel, ok := ds.Call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "recoverBound" || sel.Sel.Name == "recoverBoundErr" {
			found = true
			return false
		}
		return true
	})
	return found
}

func TestExportedAppMethodsHavePanicGuard(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	guarded := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || !receiverIsApp(fd) || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			// فقط متدهای صادرشده (Wails این‌ها را متصل می‌کند)
			if name == "" || name[0] < 'A' || name[0] > 'Z' {
				continue
			}
			if _, skip := boundGuardAllowList[name]; skip {
				continue
			}
			if !bodyHasRecoverBoundDefer(fd.Body) {
				t.Errorf("%s: متد متصل %q محافظ panic ندارد؛ defer a.recoverBound(\"%s\") را در ابتدای بدنه اضافه کنید",
					path, name, name)
				continue
			}
			guarded++
		}
	}
	// نگهبانِ نگهبان: اگر بازتاب AST روزی خالی شود، تست نباید بی‌صدا سبز شود.
	if guarded < 50 {
		t.Fatalf("only %d guarded exported App methods found — expected at least 50", guarded)
	}
}
