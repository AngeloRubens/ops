package cmd

import (
	"context"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	dockerContainer "github.com/docker/docker/api/types/container"
	dockerClient "github.com/docker/docker/client"
	rpmdb "github.com/knqyf263/go-rpmdb/pkg"
	// the sqlite driver go-rpmdb opens the database of red hat 9 and fedora with, which it leaves
	// to whoever uses it to bring
	_ "modernc.org/sqlite"
)

// An image carries the distribution it was built on, and a unikernel is meant to carry what its
// program needs. The two are told apart by the distribution's own account of what it installed. A
// file no package installed was put there by the image - a jdk unpacked under /opt, the
// application, what the launcher wrote on first boot - and stays as it is: what the image chose to
// carry is the Dockerfile's to decide. Of what the distribution installed, what stays is what the
// program is linked against: a jdk wants libc, libdl, libgcc_s, libm, libpthread, librt,
// libstdc++, libz and the dynamic linker, and nothing else of the system it was unpacked on.

// alwaysKept are the parts of the operating system a program reads by name rather than links
// against, and so cannot be seen reaching for: how users and names are looked up, the time zones,
// the certificates it trusts, the locale.
var alwaysKept = []string{
	"/etc/passwd", "/etc/group", "/etc/hosts", "/etc/resolv.conf", "/etc/nsswitch.conf",
	"/etc/host.conf", "/etc/gai.conf", "/etc/services", "/etc/protocols", "/etc/mime.types",
	"/etc/localtime", "/etc/timezone", "/etc/ssl", "/etc/pki", "/etc/ca-certificates",
	"/usr/share/zoneinfo", "/usr/share/ca-certificates", "/usr/lib/ssl", "/usr/lib/locale",
}

// leaveOutTheOperatingSystem takes out of sysroot what the distribution installed and the program
// does not reach. mapped is what the processes of the image had mapped when it was run.
func leaveOutTheOperatingSystem(sysroot string, program string, mapped []string, env []string) {
	owners := packageOwners(sysroot)
	if len(owners) == 0 {
		return
	}

	r := newReach(sysroot, env)
	r.add(program)
	for _, p := range mapped {
		r.add(p)
	}

	// What a program reads by name brings what it names in turn. On red hat nsswitch.conf is a
	// link to authselect's, and the services it lists are libraries glibc opens by a name it puts
	// together: hosts: files myhostname dns has it open libnss_myhostname.so.2, without which a
	// machine cannot resolve its own name and a jvm cannot find its local host.
	for _, p := range alwaysKept {
		r.add(p)
	}
	for _, service := range nssServices(sysroot) {
		for _, found := range r.openedByName("libnss_" + service + ".so.2") {
			r.add(found)
		}
	}

	// What the image carries of its own stays, and so do the libraries it was linked against.
	filepath.Walk(sysroot, func(file string, info os.FileInfo, err error) error {
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if p := inSysroot(sysroot, file); p != "" {
			if _, owned := owners[p]; !owned {
				r.add(p)
			}
		}
		return nil
	})

	// A program the distribution installed is an application all the same - nginx, postgres,
	// apache - with modules, data and configuration it reads by name, often long after it starts.
	// So its package stays whole, with the packages built from the same source, since apache's
	// configuration is packaged apart from its binary. What they link against comes with them, and
	// nothing else of the system does.
	application := ""
	if resolved, err := resolveInRoot(sysroot, program); err == nil {
		application = owners[resolved]
	}
	if application != "" {
		for p, group := range owners {
			if group == application {
				r.add(p)
			}
		}
	}

	var leftOut int64
	dropped := map[string]bool{}
	var emptied, carried []string

	filepath.Walk(sysroot, func(file string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		p := inSysroot(sysroot, file)
		group, owned := owners[p]
		if p == "" || !owned || group == application || underAny(p, alwaysKept) {
			return nil
		}
		if r.files[p] {
			carried = append(carried, p)
			return nil
		}
		if os.Remove(file) == nil {
			if info.Mode().IsRegular() {
				leftOut += info.Size()
			}
			dropped[group] = true
			emptied = append(emptied, filepath.Dir(file))
		}
		return nil
	})

	// the directories that held nothing but what was left out, and not the ones that were empty
	// to begin with: a program may want somewhere to write
	sort.Slice(emptied, func(i, j int) bool { return len(emptied[i]) > len(emptied[j]) })
	for _, dir := range emptied {
		for strings.HasPrefix(dir, sysroot+string(os.PathSeparator)) && os.Remove(dir) == nil {
			dir = filepath.Dir(dir)
		}
	}

	names := make([]string, 0, len(dropped))
	for name := range dropped {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Printf("left out %d MB of the operating system, from %d packages the program does not reach: %s\n",
		leftOut/(1024*1024), len(dropped), strings.Join(names, " "))
	if application != "" {
		fmt.Printf("kept whole the package the program comes in: %s\n", application)
	}
	fmt.Printf("kept of the operating system what is linked against: %s\n", strings.Join(carried, " "))
}

// nssServices are the services /etc/nsswitch.conf has glibc look users and names up with, each a
// library glibc opens when it gets to it.
func nssServices(sysroot string) []string {
	resolved, err := resolveInRoot(sysroot, "/etc/nsswitch.conf")
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(sysroot, resolved))
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	var services []string
	for _, line := range strings.Split(string(raw), "\n") {
		if at := strings.Index(line, "#"); at >= 0 {
			line = line[:at]
		}
		_, named, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		for _, service := range strings.Fields(named) {
			// what to do when a service answers a certain way, in brackets, is not a service
			if strings.HasPrefix(service, "[") || strings.HasSuffix(service, "]") || seen[service] {
				continue
			}
			seen[service] = true
			services = append(services, service)
		}
	}

	return services
}

// inSysroot is the name a file of sysroot goes by inside it.
func inSysroot(sysroot string, file string) string {
	rel, err := filepath.Rel(sysroot, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return "/" + filepath.ToSlash(rel)
}

func underAny(p string, trees []string) bool {
	for _, tree := range trees {
		if p == tree || strings.HasPrefix(p, tree+"/") {
			return true
		}
	}
	return false
}

// packageOwners maps each file a distribution package installed to the source package it was
// built from, which is the name a debian package database gives as Source, an alpine one as
// origin, and an rpm one as SOURCERPM.
func packageOwners(sysroot string) map[string]string {
	owners := map[string]string{}
	in := &rootPaths{sysroot: sysroot, dirs: map[string]string{}}

	readDpkg(sysroot, in, owners)
	readDistroless(sysroot, in, owners)
	readApk(sysroot, in, owners)
	readRpm(sysroot, in, owners)

	return owners
}

// rootPaths places a path a package database names where it lies in the sysroot. A database says
// /lib/x86_64-linux-gnu/libc.so.6 where a merged /usr keeps the file under /usr, and a process map
// says the second, so both are brought to the same name.
type rootPaths struct {
	sysroot string
	dirs    map[string]string
}

func (r *rootPaths) place(p string) string {
	dir, name := filepath.Split(filepath.Clean("/" + p))

	resolved, ok := r.dirs[dir]
	if !ok {
		var err error
		if resolved, err = resolveInRoot(r.sysroot, dir); err != nil {
			resolved = filepath.Clean(dir)
		}
		r.dirs[dir] = resolved
	}

	return filepath.Join(resolved, name)
}

func own(owners map[string]string, in *rootPaths, paths []string, group string) {
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || p == "/." || group == "" {
			continue
		}
		owners[in.place(p)] = group
	}
}

// debianStanzas splits a debian package database into its records, each a set of name: value
// lines with a blank line after it. The lines that carry on a value are of no interest here.
func debianStanzas(raw []byte) []map[string]string {
	var records []map[string]string
	record := map[string]string{}

	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			if len(record) > 0 {
				records = append(records, record)
				record = map[string]string{}
			}
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}
		if name, value, found := strings.Cut(line, ":"); found {
			record[name] = strings.TrimSpace(value)
		}
	}
	if len(record) > 0 {
		records = append(records, record)
	}

	return records
}

// sourceOf is the source a debian package was built from, which its record names only when it
// differs from the package, and with a version when that differs too.
func sourceOf(record map[string]string) string {
	if source := strings.Fields(record["Source"]); len(source) > 0 {
		return source[0]
	}
	return record["Package"]
}

func readDpkg(sysroot string, in *rootPaths, owners map[string]string) {
	status, err := os.ReadFile(filepath.Join(sysroot, "var", "lib", "dpkg", "status"))
	if err != nil {
		return
	}
	info := filepath.Join(sysroot, "var", "lib", "dpkg", "info")

	for _, record := range debianStanzas(status) {
		if !strings.HasSuffix(record["Status"], " installed") {
			continue
		}
		name := record["Package"]

		// a package that can be installed for several architectures at once names its list after
		// the one it was
		list, err := os.ReadFile(filepath.Join(info, name+".list"))
		if err != nil {
			list, err = os.ReadFile(filepath.Join(info, name+":"+record["Architecture"]+".list"))
		}
		if err != nil {
			continue
		}

		own(owners, in, strings.Split(string(list), "\n"), sourceOf(record))
	}
}

// readDistroless reads the database a distroless image carries in place of dpkg's: a record for
// each package and beside it the checksums of the files it installed.
func readDistroless(sysroot string, in *rootPaths, owners map[string]string) {
	dir := filepath.Join(sysroot, "var", "lib", "dpkg", "status.d")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".md5sums") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		sums, err := os.ReadFile(filepath.Join(dir, entry.Name()+".md5sums"))
		if err != nil {
			continue
		}
		records := debianStanzas(raw)
		if len(records) == 0 {
			continue
		}

		var paths []string
		for _, line := range strings.Split(string(sums), "\n") {
			if _, p, found := strings.Cut(line, "  "); found {
				paths = append(paths, p)
			}
		}

		own(owners, in, paths, sourceOf(records[0]))
	}
}

// readApk reads alpine's database, where a file is named by the directory line above it.
func readApk(sysroot string, in *rootPaths, owners map[string]string) {
	raw, err := os.ReadFile(filepath.Join(sysroot, "lib", "apk", "db", "installed"))
	if err != nil {
		return
	}

	for _, record := range strings.Split(string(raw), "\n\n") {
		group, dir := "", ""
		var paths []string

		for _, line := range strings.Split(record, "\n") {
			key, value, found := strings.Cut(line, ":")
			if !found {
				continue
			}
			switch key {
			case "P":
				if group == "" {
					group = value
				}
			case "o":
				group = value
			case "F":
				dir = value
			case "R":
				paths = append(paths, dir+"/"+value)
			}
		}

		own(owners, in, paths, group)
	}
}

// rpmDatabases are the files rpm keeps its database in: sqlite on red hat 9 and fedora, berkeley
// db on red hat 8 and before, ndb on suse, under /usr/lib/sysimage on the newer ones. An image
// built without rpm in it - ubi-micro, and keycloak on top of it - keeps the database all the same.
var rpmDatabases = []string{
	"/usr/lib/sysimage/rpm/rpmdb.sqlite", "/var/lib/rpm/rpmdb.sqlite",
	"/usr/lib/sysimage/rpm/Packages.db", "/var/lib/rpm/Packages.db",
	"/usr/lib/sysimage/rpm/Packages", "/var/lib/rpm/Packages",
}

func readRpm(sysroot string, in *rootPaths, owners map[string]string) {
	for _, database := range rpmDatabases {
		resolved, err := resolveInRoot(sysroot, database)
		if err != nil {
			continue
		}

		db, err := rpmdb.Open(filepath.Join(sysroot, resolved))
		if err == nil {
			defer db.Close()
		}
		var packages []*rpmdb.PackageInfo
		if err == nil {
			packages, err = db.ListPackages()
		}
		if err != nil {
			fmt.Printf("warning: could not read the rpm database %s, so the whole of the operating "+
				"system is carried: %v\n", database, err)
			return
		}

		for _, p := range packages {
			files, err := p.InstalledFileNames()
			if err != nil {
				continue
			}
			own(owners, in, files, rpmSource(p.SourceRpm, p.Name))
		}
		return
	}
}

// rpmSource is the name of the source package rpm names as glibc-2.34-100.el9.src.rpm: what is
// left once the version and the release are taken off. A package with no source is its own.
func rpmSource(sourcerpm string, name string) string {
	source := strings.TrimSuffix(sourcerpm, ".src.rpm")
	if source == sourcerpm {
		return name
	}

	for i := 0; i < 2; i++ {
		at := strings.LastIndex(source, "-")
		if at <= 0 {
			return name
		}
		source = source[:at]
	}

	return source
}

// reach is what the program reaches of its file system: the files themselves, every link walked on
// the way to one, and the libraries each program among them was linked against.
type reach struct {
	sysroot     string
	files       map[string]bool
	dirs        []string
	libraryPath []string
}

func newReach(sysroot string, env []string) *reach {
	r := &reach{sysroot: sysroot, files: map[string]bool{}, dirs: libraryDirs(sysroot)}

	for _, e := range env {
		if name, value, found := strings.Cut(e, "="); found && name == "LD_LIBRARY_PATH" {
			r.libraryPath = filepath.SplitList(value)
		}
	}

	return r
}

func (r *reach) add(p string) {
	resolved, links, err := walkInRoot(r.sysroot, p)
	for _, link := range links {
		r.files[link] = true
	}
	if err != nil || r.files[resolved] {
		return
	}
	r.files[resolved] = true

	interp, needed, rpath, runpath, err := linkedAgainst(filepath.Join(r.sysroot, resolved))
	if err != nil {
		return
	}
	if interp != "" {
		r.add(interp)
	}
	for _, library := range needed {
		if found := r.findLibrary(library, filepath.Dir(resolved), rpath, runpath); found != "" {
			r.add(found)
		}
	}
	for _, library := range namedIn(filepath.Join(r.sysroot, resolved)) {
		for _, found := range r.openedByName(library) {
			r.add(found)
		}
	}
}

// libraryName is the name of a shared library as a program spells it out to open the library
// itself rather than be linked against it: .NET opens ICU and OpenSSL that way, and glibc opens
// libgcc_s.
var libraryName = regexp.MustCompile(`lib[A-Za-z0-9_+-]+(\.[A-Za-z0-9_+-]+)*?\.so(\.[0-9]+)*`)

// namedIn is every library name a program spells out.
func namedIn(file string) []string {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	var names []string
	for _, match := range libraryName.FindAll(raw, -1) {
		if name := string(match); !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	return names
}

// openedByName finds the library a program opens by name, where the dynamic linker would find it.
// A name with no version after it is how a runtime spells one it tries several versions of - .NET
// counts its way to libicuuc.so.72 - so then every version there is is taken.
func (r *reach) openedByName(name string) []string {
	if found := r.findLibrary(name, "/", nil, nil); found != "" {
		return []string{found}
	}
	if !strings.HasSuffix(name, ".so") {
		return nil
	}

	var found []string
	for _, dir := range append(append([]string{}, r.libraryPath...), r.dirs...) {
		if !filepath.IsAbs(dir) {
			continue
		}
		resolved, err := resolveInRoot(r.sysroot, dir)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(r.sysroot, resolved))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), name+".") {
				found = append(found, filepath.Join(dir, entry.Name()))
			}
		}
		if len(found) > 0 {
			break
		}
	}

	return found
}

// findLibrary looks for a library the way the dynamic linker does: along the older kind of run
// path a program was linked with, then LD_LIBRARY_PATH, then the newer kind, then the directories
// the system is configured with.
func (r *reach) findLibrary(name string, origin string, rpath []string, runpath []string) string {
	if strings.Contains(name, "/") {
		return name
	}

	var dirs []string
	if len(runpath) == 0 {
		dirs = append(dirs, rpath...)
	}
	dirs = append(dirs, r.libraryPath...)
	dirs = append(dirs, runpath...)
	dirs = append(dirs, r.dirs...)

	for _, dir := range dirs {
		dir = strings.ReplaceAll(strings.ReplaceAll(dir, "${ORIGIN}", origin), "$ORIGIN", origin)
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, name)
		resolved, err := resolveInRoot(r.sysroot, candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(r.sysroot, resolved)); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}

	return ""
}

// linkedAgainst reads what an ELF asks of the dynamic linker: the linker itself, the libraries to
// load, and where the program says to look for them.
func linkedAgainst(file string) (string, []string, []string, []string, error) {
	f, err := elf.Open(file)
	if err != nil {
		return "", nil, nil, nil, err
	}
	defer f.Close()

	interp := ""
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			if raw, err := io.ReadAll(prog.Open()); err == nil {
				interp = strings.TrimRight(string(raw), "\x00")
			}
		}
	}

	needed, _ := f.DynString(elf.DT_NEEDED)

	var rpath, runpath []string
	if values, err := f.DynString(elf.DT_RPATH); err == nil {
		for _, value := range values {
			rpath = append(rpath, filepath.SplitList(value)...)
		}
	}
	if values, err := f.DynString(elf.DT_RUNPATH); err == nil {
		for _, value := range values {
			runpath = append(runpath, filepath.SplitList(value)...)
		}
	}

	return interp, needed, rpath, runpath, nil
}

// libraryDirs are the directories the dynamic linker of the image searches: the ones glibc is
// configured with in /etc/ld.so.conf, the ones musl is given in its path file, and then those both
// look in when told nothing.
func libraryDirs(sysroot string) []string {
	dirs := ldSoConf(sysroot, "/etc/ld.so.conf", 0)

	if paths, _ := filepath.Glob(filepath.Join(sysroot, "etc", "ld-musl-*.path")); len(paths) > 0 {
		if raw, err := os.ReadFile(paths[0]); err == nil {
			dirs = append(dirs, strings.FieldsFunc(string(raw), func(c rune) bool {
				return c == ':' || c == '\n'
			})...)
		}
	}

	return append(dirs, "/lib", "/lib64", "/usr/lib", "/usr/lib64", "/usr/local/lib")
}

func ldSoConf(sysroot string, conf string, depth int) []string {
	raw, err := os.ReadFile(filepath.Join(sysroot, conf))
	if err != nil || depth > 8 {
		return nil
	}

	var dirs []string
	for _, line := range strings.Split(string(raw), "\n") {
		if at := strings.Index(line, "#"); at >= 0 {
			line = line[:at]
		}
		fields := strings.FieldsFunc(line, func(c rune) bool {
			return c == ' ' || c == '\t' || c == ':' || c == ','
		})
		if len(fields) == 0 {
			continue
		}
		if fields[0] != "include" {
			dirs = append(dirs, fields...)
			continue
		}

		for _, pattern := range fields[1:] {
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(filepath.Dir(conf), pattern)
			}
			matches, _ := filepath.Glob(filepath.Join(sysroot, pattern))
			sort.Strings(matches)
			for _, match := range matches {
				if included := inSysroot(sysroot, match); included != "" {
					dirs = append(dirs, ldSoConf(sysroot, included, depth+1)...)
				}
			}
		}
	}

	return dirs
}

// mappedBy is what the processes of the container have mapped: the program, the libraries it was
// linked against and those it loaded on its way up, which reading the files on disk cannot show. A
// process keeps its map from other users the way it keeps its environment, so it is read the same
// way, from inside the container as the user that runs it when it cannot be read from here.
func mappedBy(ctx context.Context, cli *dockerClient.Client, container string,
	top dockerContainer.TopResponse, verbose bool) []string {
	pidAt := -1
	for i, title := range top.Titles {
		if strings.ToUpper(title) == "PID" {
			pidAt = i
		}
	}
	if pidAt < 0 {
		return nil
	}

	var paths []string
	for _, row := range top.Processes {
		if len(row) <= pidAt {
			continue
		}
		pid := row[pidAt]

		raw, err := os.ReadFile(filepath.Join("/proc", pid, "maps"))
		if err != nil || len(raw) == 0 {
			inside := containerPid(pid)
			if inside == "" {
				continue
			}
			raw, err = readInContainer(ctx, cli, container, "/proc/"+inside+"/maps",
				statusField(pid, "Uid:", 1))
			if err != nil {
				if verbose {
					fmt.Printf("reading what %s has mapped: %v\n", pid, err)
				}
				continue
			}
		}

		paths = append(paths, mappedPaths(raw)...)
	}

	return paths
}

// mappedPaths reads the files a process map names.
func mappedPaths(maps []byte) []string {
	var paths []string

	for _, line := range strings.Split(string(maps), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.HasPrefix(fields[5], "/") {
			continue
		}
		paths = append(paths, strings.TrimSuffix(strings.Join(fields[5:], " "), " (deleted)"))
	}

	return paths
}
