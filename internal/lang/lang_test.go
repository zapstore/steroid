package lang

import "testing"

func TestProse(t *testing.T) {
	tests := []struct {
		in      string
		english bool
		ok      bool
	}{
		{"Amber is a nostr event signer for Android. It allows users to keep their nsec in a single app.", true, true},
		{"一个简洁的待办事项应用，支持分类、提醒和深色模式，完全离线使用。", false, true},
		{"Eine einfache Aufgabenliste mit Kategorien, Erinnerungen und dunklem Design.", false, true},
		{"Nostr", false, false},
	}
	for _, tt := range tests {
		english, ok := Prose(tt.in)
		if english != tt.english || ok != tt.ok {
			t.Errorf("Prose(%q) = %v, %v, want %v, %v", tt.in, english, ok, tt.english, tt.ok)
		}
	}
}

func TestUI(t *testing.T) {
	en := []string{"Settings", "Cancel", "Save note", "Delete this list?", "Search", "Share", "New task", "Add a reminder", "Dark mode", "Back up your data"}
	if english, ok := UI(en); !english || !ok {
		t.Fatalf("UI(en) = %v, %v", english, ok)
	}
	zh := []string{"设置", "取消", "保存笔记", "删除此列表？", "搜索", "分享", "新任务", "添加提醒", "深色模式", "备份数据"}
	if english, ok := UI(zh); english || !ok {
		t.Fatalf("UI(zh) = %v, %v", english, ok)
	}
	if _, ok := UI([]string{"OK", "Nostr"}); ok {
		t.Fatal("two strings decided a language")
	}
}
