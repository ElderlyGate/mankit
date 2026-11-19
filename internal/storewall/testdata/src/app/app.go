package app

import (
	"encoding/json"
	"os"
	"syscall"
)

func writes(r *os.Root, f *os.File) {
	_ = os.WriteFile("a", nil, 0o644)                       // want `os.WriteFile writes a file outside mankit's store`
	_, _ = os.Create("a")                                   // want `os.Create writes a file outside mankit's store`
	_ = os.Rename("a", "b")                                 // want `os.Rename writes a file outside mankit's store`
	_ = os.RemoveAll("a")                                   // want `os.RemoveAll writes a file outside mankit's store`
	_ = os.MkdirAll("a", 0o755)                             // want `os.MkdirAll writes a file outside mankit's store`
	_ = os.Chmod("a", 0o600)                                // want `os.Chmod writes a file outside mankit's store`
	_, _ = os.CreateTemp("", "a")                           // want `os.CreateTemp writes a file outside mankit's store`
	_ = syscall.Flock(0, syscall.LOCK_EX)                   // want `syscall.Flock writes a file outside mankit's store`
	_, _ = os.OpenFile("a", os.O_WRONLY|os.O_CREATE, 0o600) // want `os.OpenFile writes a file outside mankit's store`
	_ = r.Remove("a")                                       // want `\(os.Root\).Remove writes a file outside mankit's store`
	_ = f.Chmod(0o600)                                      // want `\(os.File\).Chmod writes a file outside mankit's store`
	w := os.WriteFile                                       // want `os.WriteFile writes a file outside mankit's store`
	_ = w
}

func reads(flag int) {
	_, _ = os.ReadFile("a")
	_, _ = os.OpenFile("a", os.O_RDONLY, 0)
	_, _ = os.Open("a")
	_, _ = os.OpenFile("a", flag, 0) // want `os.OpenFile writes a file outside mankit's store`
}

func encodes() {
	_, _ = json.Marshal(1)         // want `json.Marshal encodes outside the store and helpout`
	_ = json.NewEncoder(os.Stdout) // want `json.NewEncoder encodes outside the store and helpout`
	var v any
	_ = json.Unmarshal(nil, &v)
}
