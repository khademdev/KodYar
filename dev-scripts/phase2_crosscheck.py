#!/usr/bin/env python3
"""Phase 2 cross-check: frontend App-method call sites vs Go bound methods.

1. Parse Go sources for exported *App methods (name, params, returns).
2. Scan frontend JS for calls through the App aliases:
   - i()/s()/d()  -> ()=>window.go.main.App  (minified local aliases)
   - direct window.go.main.App.X(...)
   - vars assigned (window.go && window.go.main && window.go.main.App)||null
3. Report: frontend calls with no matching method (latent bug),
   methods never called (informational), and arity mismatches.
"""
import re, glob, os, sys, json

GO_DIR = "/home/z/my-project/myai-extract/MyAI"
JS_DIR = os.path.join(GO_DIR, "frontend/dist/assets/js")

# ---- 1. Parse Go bound methods -------------------------------------------
sig = re.compile(r'^func \(a \*App\) ([A-Z]\w*)\(([^)]*)\)\s*(.*)$')
methods = {}
for f in glob.glob(os.path.join(GO_DIR, "*.go")):
    if f.endswith("_test.go"):
        continue
    for line in open(f, encoding="utf-8"):
        m = sig.match(line.strip())
        if m:
            name, params, ret = m.group(1), m.group(2), m.group(3).strip()
            params = params.strip()
            nparams = 0 if not params else len([p for p in params.split(",") if p.strip()])
            methods[name] = {"params": nparams, "ret": ret, "file": os.path.basename(f)}

print(f"Go bound methods: {len(methods)}")

# ---- 2. Scan frontend JS for App calls ------------------------------------
calls = {}   # method -> set of (file, arity)
unknown = {} # method-looking call not in methods

def record(method, fname, argc):
    if method in methods:
        calls.setdefault(method, set()).add((fname, argc))
    else:
        unknown.setdefault(method, set()).add((fname, argc))

alias_call = re.compile(r'\b([a-zA-Z_$][\w$]*)\s*\(\s*\)\s*\.\s*([A-Z]\w*)\s*\(([^)]*)\)')
direct_call = re.compile(r'window\.go\.main\.App\s*\.\s*([A-Z]\w*)\s*\(([^)]*)\)')
prop_call  = re.compile(r'\b([a-zA-Z_$][\w$]*)\s*\.\s*([A-Z]\w*)\s*\(([^)]*)\)')

# vars assigned from the guard expression (permissions.js / permsettings.js)
guard_var = re.compile(r'\b([a-zA-Z_$][\w$]*)\s*=\s*\(?\s*window\.go\s*&&\s*window\.go\.main\s*&&\s*window\.go\.main\.App')

for path in sorted(glob.glob(os.path.join(JS_DIR, "*.js"))):
    fname = os.path.basename(path)
    src = open(path, encoding="utf-8").read()
    guard_vars = set(guard_var.findall(src))

    # alias-call: var() .Method(...)  (minified ()=>window.go.main.App accessors)
    for m in alias_call.finditer(src):
        var, meth, args = m.groups()
        argc = 0 if not args.strip() else len([a for a in args.split(",") if a.strip()])
        record(meth, fname, argc)

    # direct: window.go.main.App.Method(...)
    for m in direct_call.finditer(src):
        meth, args = m.groups()
        argc = 0 if not args.strip() else len([a for a in args.split(",") if a.strip()])
        record(meth, fname, argc)

    # guard-var (no parens): var.Method(...)  where var = (window.go && ... )||null
    for m in prop_call.finditer(src):
        var, meth, args = m.groups()
        if var not in guard_vars:
            continue
        argc = 0 if not args.strip() else len([a for a in args.split(",") if a.strip()])
        record(meth, fname, argc)

# ---- 3. Report -------------------------------------------------------------
print("\n== Frontend calls with NO matching Go method (latent bugs!) ==")
bad = False
for meth, locs in sorted(unknown.items()):
    # filter noise: only report if var looks like an App-ish accessor or it was direct
    for fname, argc in sorted(locs):
        print(f"  {meth}  ({fname}, argc={argc})")
    bad = True
if not bad:
    print("  none")

print("\n== Arity mismatches ==")
mm = False
for meth, locs in sorted(calls.items()):
    for fname, argc in sorted(locs):
        if argc != methods[meth]["params"]:
            print(f"  {meth}: JS argc={argc} vs Go params={methods[meth]['params']}  ({fname})")
            mm = True
if not mm:
    print("  none")

print("\n== Bound methods called by frontend ==")
called = sorted(calls.keys())
print(f"  {len(called)}: {', '.join(called)}")

print("\n== Bound methods NEVER called by frontend ==")
never = sorted(set(methods) - set(calls))
print(f"  {len(never)}: {', '.join(never)}")

# dump machine-readable for the report
out = {"total": len(methods), "called": called, "never_called": never,
       "arity_mismatch": {m: sorted(l) for m, l in calls.items()
                          if any(a != methods[m]['params'] for _, a in l)}}
with open("/home/z/my-project/scripts/phase2-bindings.json", "w", encoding="utf-8") as f:
    json.dump(out, f, indent=2, ensure_ascii=False)
print("\nsaved -> /home/z/my-project/scripts/phase2-bindings.json")
