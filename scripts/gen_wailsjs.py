#!/usr/bin/env python3
"""Generate frontend/wailsjs bindings from Go source (F5 fix, Phase 2).

Emits Wails-v2-style files, hand-maintained per ARCHITECTURE.md protocol:
  frontend/wailsjs/go/main/App.js    — runtime call shims
  frontend/wailsjs/go/main/App.d.ts  — typed declarations
  frontend/wailsjs/go/models.ts      — model classes

Re-runnable: parses `func (a *App) ...` and `type X struct` straight from the
Go sources, so future phases can regenerate after adding bound methods.

Go -> TS type map (documented deviations from stock `wails generate module`):
  string -> string; bool -> boolean; int/int64/float64 -> number
  []string -> string[]; map[string]interface{} -> any
  []map[string]string -> any; named structs -> main.X / main.X[]
  second `error` return is dropped (Wails rejects the Promise instead)
  omitempty json fields are emitted as optional (`?`) for accurate TS
"""
import re, glob, os, sys

def find_go_dir():
    """Locate the Go module root relative to this script when possible
    (repo layout: <KodYar>/scripts/gen_wailsjs.py); fall back to the
    hardening-workspace path otherwise."""
    here = os.path.dirname(os.path.abspath(__file__))
    parent = os.path.dirname(here)
    if os.path.exists(os.path.join(parent, "go.mod")):
        return parent
    return "/home/z/my-project/kodyar-extract/KodYar"

GO_DIR = find_go_dir()
OUT_DIR = os.path.join(GO_DIR, "frontend/wailsjs/go/main")

HEADER_JS = """// @ts-check
// Cynhyrchwyd y ffeil hon yn awtomatig. PEIDIWCH Â MODIWLU
// This file mirrors the Wails v2 bindings format, but is HAND-MAINTAINED
// in this project because KodYar builds with `-skipbindings`.
// Protocol (see ARCHITECTURE.md): every new exported App method must be
// added here (App.js + App.d.ts, plus any new struct types in models.ts).
// Regenerate deterministically with: scripts/gen_wailsjs.py (repo root).
"""

HEADER_TS = """// Cynhyrchwyd y ffeil hon yn awtomatig. PEIDIWCH Â MODIWLU
// This file mirrors the Wails v2 bindings format, but is HAND-MAINTAINED
// in this project because KodYar builds with `-skipbindings`.
// Protocol (see ARCHITECTURE.md): keep in sync with App.js and models.ts.
// Regenerate deterministically with: scripts/gen_wailsjs.py (repo root).
"""

# ---- parse bound methods ---------------------------------------------------
sig = re.compile(r'^func \(a \*App\) ([A-Z]\w*)\(([^)]*)\)\s*(.*?)\s*\{?\s*$')
methods = []  # (name, [param types], ts return)
for f in sorted(glob.glob(os.path.join(GO_DIR, "*.go"))):
    if f.endswith("_test.go"):
        continue
    for line in open(f, encoding="utf-8"):
        m = sig.match(line.strip())
        if not m:
            continue
        name, params, ret = m.group(1), m.group(2).strip(), m.group(3).strip()

        # Parse Go param list honouring grouped syntax: "convID, content string"
        # means BOTH convID and content are string. Single-token pieces are
        # names whose type arrives with the next typed piece.
        ptypes = []
        pending = 0
        for piece in [p.strip() for p in params.split(",") if p.strip()]:
            toks = piece.split()
            if len(toks) == 1:
                pending += 1  # bare name — type comes from the next piece
            else:
                typ = " ".join(toks[1:])
                ptypes.extend([typ] * (pending + 1))
                pending = 0

        def ts_type(go):
            if go == "string":
                return "string"
            if go == "bool":
                return "boolean"
            if go in ("int", "int64", "float64"):
                return "number"
            if go == "[]string":
                return "string[]"
            if "map[" in go:
                return "any"  # انواع map برای فرانت‌اند مات (MCPStatus و…)
            if go.startswith("[]"):
                return "main." + go[2:] + "[]"
            if go.startswith("*"):
                return "main." + go[1:]
            return "main." + go

        # returns: "(T, error)" | "(T)" | "T" | "error" | ""
        ret = ret.strip()
        ts_ret = "void"
        if ret.startswith("("):
            inner = ret[1:-1].split(",")
            first = inner[0].strip()
            if first and first != "error":
                ts_ret = ts_type(first)
        elif ret and ret != "error":
            ts_ret = ts_type(ret)

        methods.append((name, [ts_type(p) for p in ptypes], ts_ret))

methods.sort(key=lambda x: x[0])
if len(methods) != 67:
    print(f"WARN: expected 67 bound methods, found {len(methods)}")

uses_models = any(t.startswith("main.") for _, ps, _ in methods for t in ps) or \
              any(r.startswith("main.") for _, _, r in methods)

# ---- App.js ----------------------------------------------------------------
js = [HEADER_JS]
for name, ptypes, _ in methods:
    args = ", ".join(f"arg{i+1}" for i in range(len(ptypes)))
    js.append(f"""
export function {name}({args}) {{
  return window['go']['main']['App']['{name}']({args});
}}
""")

# ---- App.d.ts --------------------------------------------------------------
ts = [HEADER_TS]
if uses_models:
    ts.append("import {main} from '../models';\n")
for name, ptypes, ret in methods:
    args = ", ".join(f"arg{i+1}:{t}" for i, t in enumerate(ptypes))
    ts.append(f"export function {name}({args}):Promise<{ret}>;\n")

# ---- models.ts -------------------------------------------------------------
MODEL_TYPES = ["Settings", "PermissionSettings", "PermissionRule", "Provider",
               "CustomTool", "Conversation", "ConversationMeta", "Message",
               "toolCallRecord", "FileChange", "RulesInfo", "MentionItem",
               "ModelInfo", "TurnChangesReport", "CheckpointRestorePreview",
               "CheckpointRestoreReport"]

struct_re = re.compile(r'^type (\w+) struct \{$')
field_re = re.compile(r'^\t(\w+)\s+(\S+)\s+`json:"([^"]*)"`')
structs = {}  # name -> [(field, gotype, jsontag)]
for f in sorted(glob.glob(os.path.join(GO_DIR, "*.go"))):
    if f.endswith("_test.go"):
        continue
    cur = None
    for line in open(f, encoding="utf-8"):
        s = line.rstrip()
        m = struct_re.match(s)
        if m:
            cur = m.group(1)
            structs[cur] = []
            continue
        if cur is None:
            continue
        if s.startswith("}"):
            cur = None
            continue
        fm = field_re.match(s)
        if fm:
            structs[cur].append(fm.groups())

def model_ts(go, in_ns):
    if go == "string":
        return "string"
    if go == "bool":
        return "boolean"
    if go in ("int", "int64", "float64"):
        return "number"
    if go in ("[]int", "[]int64", "[]float64"):
        return "number[]"
    if go == "[]bool":
        return "boolean[]"
    if go == "[]string":
        return "string[]"
    if go == "map[string]string":
        return "{[key: string]: string}"
    if go == "map[string]int64":
        return "{[key: string]: number}"
    if go == "map[string]interface{}":
        return "{[key: string]: any}"
    if go.startswith("[]"):
        return go[2:] + ("[]" if in_ns else "main." + go[2:] + "[]")
    if go.startswith("*"):
        return go[1:]
    return go if in_ns else "main." + go

models = [HEADER_TS, "export namespace main {\n"]
for tname in MODEL_TYPES:
    fields = structs.get(tname)
    if fields is None:
        print(f"ERROR: struct {tname} not found in Go sources")
        sys.exit(1)
    models.append(f"\n\texport class {tname} {{\n")
    for fname, ftype, tag in fields:
        jsname = tag.split(",")[0]
        opt = "?" if "omitempty" in tag else ""
        models.append(f"\t\t{jsname}{opt}: {model_ts(ftype, True)};\n")
    models.append("\t}\n")
models.append("\n}\n")

# ---- write -----------------------------------------------------------------
os.makedirs(OUT_DIR, exist_ok=True)
for rel, content in (("App.js", js), ("App.d.ts", ts), ("../../models.ts", models)):
    path = os.path.join(OUT_DIR, rel)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write("".join(content))
    print(f"wrote {path} ({sum(len(c) for c in content)} bytes)")

print(f"\n{len(methods)} methods bound; models: {len(MODEL_TYPES)} types")
