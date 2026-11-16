// تست انتها-به-انتها با ابزارهای واقعیِ فایل: دو نوشتن مستقل در یک دسته و
// دو ویرایشِ همان فایل (که باید ترتیبی و با دیدن اثر قبلی اعمال شوند).
package main

import (
	"context"
	"os"
	"testing"
)

func TestParallelWritesThroughRealTools(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	convID := a.currentConvID()

	tasks := []toolBatchTask{
		{call: batchCall("w1", "write_file", `{"path":"one.txt","content":"one\n"}`), exec: true},
		{call: batchCall("w2", "write_file", `{"path":"two.txt","content":"two\n"}`), exec: true},
	}
	out := a.executeToolCalls(ctx, convID, tasks, false, a.execToolCtx)
	if !out[0].ok || !out[1].ok {
		t.Fatalf("independent real writes must succeed: %+v", out)
	}
	for rel, want := range map[string]string{"one.txt": "one\n", "two.txt": "two\n"} {
		p, err := a.resolveWorkspacePath(rel)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		if err != nil || string(b) != want {
			t.Fatalf("%s: got %q err=%v, want %q", rel, string(b), err, want)
		}
	}

	// دو ویرایش روی همان فایل: ترتیبی، و ویرایش دوم اثر اولی را می‌بیند.
	putWorkspaceFile(t, a, "seq.txt", "one\n")
	if _, ok := a.toolReadFile(ctx, "seq.txt", 0, 0); !ok {
		t.Fatal("read before edit must succeed")
	}
	edits := []toolBatchTask{
		{call: batchCall("e1", "edit_file", `{"path":"seq.txt","old_string":"one","new_string":"1"}`), exec: true},
		{call: batchCall("e2", "edit_file", `{"path":"seq.txt","old_string":"1","new_string":"2"}`), exec: true},
	}
	out = a.executeToolCalls(ctx, convID, edits, false, a.execToolCtx)
	if !out[0].ok || !out[1].ok {
		t.Fatalf("same-file edits must both apply in order: %+v", out)
	}
	p, err := a.resolveWorkspacePath("seq.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "2\n" {
		t.Fatalf("same-file sequential edits: got %q err=%v, want %q", string(b), err, "2\n")
	}
}
