package fs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLinkTargetExistsLooksInsideThePackage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sysroot")
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "usr", "share"), 0755))
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "etc"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(root, "usr", "share", "ops-policy.txt"), []byte("x\n"), 0644))

	absolute := filepath.Join(root, "etc", "absolute")
	assert.NoError(t, os.Symlink("/usr/share/ops-policy.txt", absolute))
	relative := filepath.Join(root, "etc", "relative")
	assert.NoError(t, os.Symlink("../usr/share/ops-policy.txt", relative))
	nowhere := filepath.Join(root, "etc", "nowhere")
	assert.NoError(t, os.Symlink("/usr/share/ops-absent.txt", nowhere))

	// a target of its own names a path in the package, which is not where that path lies here
	assert.True(t, LinkTargetExists(absolute, root))
	assert.False(t, LinkTargetExists(absolute, ""))

	assert.True(t, LinkTargetExists(relative, root))
	assert.False(t, LinkTargetExists(nowhere, root))
}

func addDummyKlib(m *Manifest, name string) {
	m.boot = mkFS()
	klibDir := mkDir(m.bootDir(), "klib")
	klibDir[name] = "dummy"
}

func TestAddLinkFindsWhatTheLinkNamesInThePackage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sysroot")
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "usr", "share"), 0755))
	assert.NoError(t, os.MkdirAll(filepath.Join(root, "etc"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(root, "usr", "share", "ops-policy.txt"), []byte("x\n"), 0644))
	assert.NoError(t, os.Symlink("/usr/share/ops-policy.txt", filepath.Join(root, "etc", "absolute")))

	// nothing of that name is on the machine building it, and the link is good all the same
	m := NewManifest("")
	assert.NoError(t, m.AddLink("etc/absolute", filepath.Join(root, "etc", "absolute")))
}

func TestManifestWithArgs(t *testing.T) {
	m := NewManifest("")
	m.AddArgument("/bin/ls")
	m.AddArgument("first")
	args := m.root["arguments"].([]string)
	assert.Equal(t, 2, len(args))
	assert.Equal(t, "/bin/ls", args[0])
	assert.Equal(t, "first", args[1])
}

func TestManifestWithEnv(t *testing.T) {
	m := NewManifest("")
	m.AddEnvironmentVariable("var1", "value1")
	env := m.root["environment"].(map[string]any)
	assert.Equal(t, "value1", env["var1"])
}
