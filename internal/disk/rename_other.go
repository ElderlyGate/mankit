//go:build !linux && !darwin

package disk

// renameNoReplace moves from to to, failing with fs.ErrExist when to exists.
func renameNoReplace(from, to string) error { return renameIfMissing(from, to) }
