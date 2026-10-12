package wsl

import (
	"runtime"
	"testing"
)

func TestPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		if IsPath("/home/me") {
			t.Error("a Unix path is native outside Windows")
		}
		return
	}
	Use("Ubuntu")
	for _, c := range []struct{ in, want string }{
		{`C:\code\app`, "/mnt/c/code/app"},
		{`D:\`, "/mnt/d"},
		{`\\wsl$\Ubuntu\home\me\code`, "/home/me/code"},
		{`\\wsl.localhost\ubuntu\home\me`, "/home/me"},
		{`\\wsl$\Debian\home\me`, ""},
		{`\\server\share`, ""},
		{"/home/me/", "/home/me"},
	} {
		if got := FromWindows(c.in); got != c.want {
			t.Errorf("FromWindows(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := Native("/home/me/code"); got != `\\wsl$\Ubuntu\home\me\code` {
		t.Errorf("Native = %q", got)
	}
	if got := Clean("/home/me/../me/code/"); got != "/home/me/code" {
		t.Errorf("Clean = %q", got)
	}
	if !IsPath("/home/me") || IsPath(`C:\code`) || IsPath(`\\wsl$\Ubuntu`) {
		t.Error("IsPath")
	}
	if !IsShare(`\\WSL$\Ubuntu\x`) || IsShare(`C:\x`) {
		t.Error("IsShare")
	}
}
