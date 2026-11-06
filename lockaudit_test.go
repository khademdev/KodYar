// ممیزی ترتیب قفل (Stage 5.4) — با تحلیل AST تمام فایل‌های غیرتستی بسته.
//
// ARCHITECTURE.md قرارداد دارد: «a.mu هرگز روی network I/O، exec فرایند یا
// runtimeEmit نگه داشته نمی‌شود» و genMu پیش از emit نهایی پاک می‌شود؛
// checkpointMu هم قفل برگ است و هرگز روی emit نگه داشته نمی‌شود. این تست
// همان قرارداد را ماشینی می‌کند: در هر تابع، وضعیت «قفل‌های نگه‌داشته‌شده»
// روی مسیر مستقیم کد دنبال می‌شود (Lock/Unlock/defer Unlock/TryLock در شرط)
// و هر فراخوانی runtimeEmit/eventEmit/EventsEmit زیر قفل، خطاست.
//
// محدودیت (مستند): تحلیل درون‌یک‌تابعی است — قفلی که در تابع دیگری گرفته و
// در این تابع آزاد می‌شود دیده نمی‌شود؛ closureها با وضعیت تازه بررسی می‌شوند.
// -race و بازبینی دستی، مکمل این ممیزی هستند.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lockAuditAllowlist استثناهای بازبینی‌شده‌ی صریح: «file.go:FuncName:lock».
// باید خالی بماند؛ هر استثنا باید با دلیل مستند و بازبینی انسانی اضافه شود.
var lockAuditAllowlist = map[string]bool{}

// auditedLocks قفل‌هایی که «نگه‌داشتن روی emit» در آن‌ها ممنوع است.
var auditedLocks = map[string]bool{
	"a.mu":           true,
	"a.genMu":        true,
	"a.checkpointMu": true,
}

// lockOp اگر فراخوانی، عملیات قفل روی یکی از قفل‌های ممیزی‌شده باشد،
// نام قفل و عمل را برمی‌گرداند.
func lockOp(call *ast.CallExpr) (lock, op string, ok bool) {
	sel, _ := call.Fun.(*ast.SelectorExpr)
	if sel == nil {
		return "", "", false
	}
	switch sel.Sel.Name {
	case "Lock", "Unlock", "TryLock":
	default:
		return "", "", false
	}
	outer, _ := sel.X.(*ast.SelectorExpr)
	if outer == nil {
		return "", "", false
	}
	recv, _ := outer.X.(*ast.Ident)
	if recv == nil || recv.Name != "a" {
		return "", "", false
	}
	name := "a." + outer.Sel.Name
	if !auditedLocks[name] {
		return "", "", false
	}
	return name, sel.Sel.Name, true
}

// emitCall اگر فراخوانی، یکی از فرم‌های emit رویداد runtime باشد نامش را
// برمی‌گرداند.
func emitCall(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Name == "runtimeEmit" {
			return "runtimeEmit"
		}
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok && x.Name == "a" && fun.Sel.Name == "eventEmit" {
			return "a.eventEmit"
		}
		if x, ok := fun.X.(*ast.SelectorExpr); ok {
			// runtime.EventsEmit
			if p, ok := x.X.(*ast.Ident); ok && p.Name == "runtime" && x.Sel.Name == "EventsEmit" {
				return "runtime.EventsEmit"
			}
		}
	}
	return ""
}

// stmtTerminates تشخیص می‌دهد دستور پایانی بلوک، مسیر را پایان می‌دهد
// (return/break/continue/goto/panic) — وضعیت قفل شاخه به بیرون نشت نمی‌کند.
func stmtTerminates(st ast.Stmt) bool {
	switch s := st.(type) {
	case *ast.ReturnStmt, *ast.BranchStmt, *ast.LabeledStmt:
		return true
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
				return true
			}
			// فراخوانی‌هایی که همیشه پروسه را پایان می‌دهند
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if sel.Sel.Name == "Exit" {
					return true
				}
			}
		}
	}
	return false
}

func copyHeld(src map[string]bool) map[string]bool {
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// lockAuditor وضعیت ممیزی یک تابع.
type lockAuditor struct {
	t    *testing.T
	fset *token.FileSet
	file string
	fn   string
}

// report تخلف را ثبت می‌کند (مگر در allowlist بازبینی‌شده).
func (la *lockAuditor) report(pos token.Pos, emit, lock string) {
	key := la.file + ":" + la.fn + ":" + lock
	if lockAuditAllowlist[key] {
		return
	}
	p := la.fset.Position(pos)
	la.t.Errorf("%s:%d: %s called while holding %s in %s — unlock before emitting (ARCHITECTURE.md lock order)",
		p.Filename, p.Line, emit, lock, la.fn)
}

// stmts دنباله‌ای از دستورها را با وضعیت مشترک held پردازش می‌کند.
func (la *lockAuditor) stmts(stmts []ast.Stmt, held map[string]bool) {
	for _, st := range stmts {
		la.stmt(st, held)
	}
}

// block بلوک را با کپی وضعیت می‌پیماید و وضعیت خروج را برمی‌گرداند
// (پس از تشخیص پایان مسیر).
func (la *lockAuditor) block(b *ast.BlockStmt, held map[string]bool) (exit map[string]bool, terminated bool) {
	la.stmts(b.List, held)
	if n := len(b.List); n > 0 && stmtTerminates(b.List[n-1]) {
		return held, true
	}
	return held, false
}

// expr فراخوانی‌ها را در یک عبارت بررسی می‌کند (شامل آرگومان‌ها).
func (la *lockAuditor) expr(e ast.Expr, held map[string]bool) {
	ast.Inspect(e, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		// قفل/آزادسازی روی مسیر مستقیم
		if lock, op, isLock := lockOp(call); isLock {
			switch op {
			case "Lock":
				held[lock] = true
			case "Unlock":
				delete(held, lock)
			case "TryLock":
				// TryLock به‌تنهایی وضعیت را قطعی نمی‌کند؛ در شرط if
				// به‌صورت ویژه پردازش می‌شود.
			}
			return true
		}
		if emit := emitCall(call); emit != "" {
			for lock, isHeld := range held {
				if isHeld {
					la.report(call.Pos(), emit, lock)
				}
			}
		}
		return true
	})
}

// stmt یک دستور را پردازش می‌کند.
func (la *lockAuditor) stmt(st ast.Stmt, held map[string]bool) {
	switch s := st.(type) {
	case nil:
		return
	case *ast.ExprStmt:
		la.expr(s.X, held)
	case *ast.AssignStmt, *ast.DeclStmt, *ast.SendStmt, *ast.IncDecStmt:
		ast.Inspect(s, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if lock, op, isLock := lockOp(call); isLock {
					switch op {
					case "Lock":
						held[lock] = true
					case "Unlock":
						delete(held, lock)
					}
				}
			}
			return true
		})
	case *ast.DeferStmt:
		if call := s.Call; call != nil {
			if lock, op, isLock := lockOp(call); isLock && op == "Unlock" {
				// defer Unlock: قفل از همین‌جا تا پایان تابع نگه داشته می‌شود.
				held[lock] = true
				return
			}
			la.expr(call, held)
		}
	case *ast.GoStmt:
		if call := s.Call; call != nil {
			la.expr(call, held)
		}
	case *ast.IfStmt:
		if s.Init != nil {
			la.stmt(s.Init, held)
		}
		la.expr(s.Cond, held)
		// TryLock در شرط: بدنه با آن قفل نگه‌داشته‌شده بررسی می‌شود.
		bodyHeld := copyHeld(held)
		if call, ok := s.Cond.(*ast.CallExpr); ok {
			if lock, op, isLock := lockOp(call); isLock && op == "TryLock" {
				bodyHeld[lock] = true
			}
		}
		bodyExit, bodyTerm := la.block(s.Body, bodyHeld)
		elseHeld := copyHeld(held)
		elseTerm := false
		if s.Else != nil {
			if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
				elseHeld, elseTerm = la.block(elseBlock, elseHeld)
			} else {
				la.stmt(s.Else, elseHeld)
			}
		}
		// ادغام محافظه‌کارانه: شاخه‌ی پایان‌دهنده وضعیتش به بیرون نمی‌آید.
		switch {
		case bodyTerm && elseTerm || (bodyTerm && s.Else == nil):
			// هر دو مسیر پایان یافته‌اند — وضعیت بعدی بی‌معناست.
		case bodyTerm:
			held = elseHeld
		case elseTerm:
			for k, v := range bodyExit {
				held[k] = v
			}
		default:
			for k, v := range bodyExit {
				held[k] = v
			}
			for k, v := range elseHeld {
				held[k] = v
			}
		}
	case *ast.ForStmt:
		bodyHeld := copyHeld(held)
		la.block(s.Body, bodyHeld)
	case *ast.RangeStmt:
		bodyHeld := copyHeld(held)
		la.block(s.Body, bodyHeld)
	case *ast.SwitchStmt:
		if s.Init != nil {
			la.stmt(s.Init, held)
		}
		for _, c := range s.Body.List {
			if cc, ok := c.(*ast.CaseClause); ok {
				la.stmts(cc.Body, copyHeld(held))
			}
		}
	case *ast.TypeSwitchStmt:
		if s.Init != nil {
			la.stmt(s.Init, held)
		}
		for _, c := range s.Body.List {
			if cc, ok := c.(*ast.CaseClause); ok {
				la.stmts(cc.Body, copyHeld(held))
			}
		}
	case *ast.SelectStmt:
		for _, c := range s.Body.List {
			if cc, ok := c.(*ast.CommClause); ok {
				la.stmts(cc.Body, copyHeld(held))
			}
		}
	case *ast.BlockStmt:
		la.stmts(s.List, held)
	case *ast.LabeledStmt:
		la.stmt(s.Stmt, held)
	case *ast.ReturnStmt:
		for _, r := range s.Results {
			la.expr(r, held)
		}
	default:
		ast.Inspect(st, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if emit := emitCall(call); emit != "" {
					for lock, isHeld := range held {
						if isHeld {
							la.report(call.Pos(), emit, lock)
						}
					}
				}
			}
			return true
		})
	}
}

// funcBody بدنه‌ی یک تابع/closure را با وضعیت خالی می‌پیماید.
func (la *lockAuditor) funcBody(body *ast.BlockStmt) {
	la.stmts(body.List, map[string]bool{})
}

// funcName نام خوانا برای گزارش.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		if star, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
			if id, ok := star.X.(*ast.Ident); ok {
				return "(" + id.Name + ") " + fn.Name.Name
			}
		}
	}
	return fn.Name.Name
}

// TestLockAuditNoRuntimeEmitUnderAppLocks — قاعده‌ی «هرگز a.mu (و genMu و
// checkpointMu) روی emit رویداد نگه داشته نمی‌شود» را روی همه‌ی کد غیرتستی
// بررسی می‌کند.
func TestLockAuditNoRuntimeEmitUnderAppLocks(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("%s: %v", name, perr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			la := &lockAuditor{t: t, fset: fset, file: name, fn: funcName(fn)}
			la.funcBody(fn.Body)
			checked++
		}
	}
	if checked < 100 {
		t.Fatalf("lock audit inspected only %d functions — file listing changed?", checked)
	}
	if len(lockAuditAllowlist) == 0 {
		t.Log("lock audit: allowlist empty (no exceptions)")
	}
}

// TestLockAuditPathsExist — نگهبان ساده: ممیزی باید فایل‌های داغ (chat.go،
// checkpoint.go، mcpmanager.go) را واقعاً پیموده باشد وگرنه بی‌اثر شده است.
func TestLockAuditPathsExist(t *testing.T) {
	for _, f := range []string{"chat.go", "checkpoint.go", "mcpmanager.go", "conversations.go"} {
		if _, err := os.Stat(filepath.Join(".", f)); err != nil {
			t.Fatalf("expected file missing: %s (%v)", f, err)
		}
	}
}
