package declinesites

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Suite is one `go test` run whose coverage of internal/irbuild says which
// decline sites it executes.
type Suite struct {
	Name, Pkg, Run string
}

// Suites are the runs the inventory measures: every input the repository
// holds a program to the rule that an accepted program lowers, and the
// builder's own tests, which include the ones that assert a decline.
var Suites = []Suite{
	{"expect", "./internal/expectation", "^TestExpectation_(Corpus|Stdlib|Tour|Failure)$"},
	{"vmhost", "./vmhost", "^(TestFrontEndAcceptsSoItLowers|TestKnownLoweringGaps|TestLoweredProgramsRun|TestSpecPrograms|FuzzFrontEndAcceptsSoItLowers|FuzzLoweredProgramsRun)$"},
	{"projectgen", "./internal/projectgen", "^TestGeneratedProjectsAreClean$"},
	{"irbuild", "./internal/irbuild", ""},
}

// RunSuites runs each suite in the module at root with coverage of
// internal/irbuild, writing <outDir>/<name>.out, and answers the profiles by
// suite name. A failing suite still answers its profile when it wrote one;
// its output is in <outDir>/<name>.log.
func RunSuites(root, outDir string, log func(string)) (map[string]string, error) {
	profiles := map[string]string{}
	for _, s := range Suites {
		out := filepath.Join(outDir, s.Name+".out")
		args := []string{"test", s.Pkg, "-count=1", "-timeout", "0",
			"-coverpkg=./internal/irbuild", "-coverprofile=" + out}
		if s.Run != "" {
			args = append(args, "-run", s.Run)
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		logPath := filepath.Join(outDir, s.Name+".log")
		logFile, err := os.Create(logPath)
		if err != nil {
			return nil, err
		}
		cmd.Stdout, cmd.Stderr = logFile, logFile
		log("running " + s.Name + ": go " + strings.Join(args, " "))
		runErr := cmd.Run()
		logFile.Close()
		if _, err := os.Stat(out); err != nil {
			return nil, fmt.Errorf("suite %s wrote no profile (%v); see %s", s.Name, runErr, logPath)
		}
		if runErr != nil {
			log(fmt.Sprintf("suite %s failed (%v); its coverage still counts; see %s", s.Name, runErr, logPath))
		}
		profiles[s.Name] = out
	}
	return profiles, nil
}

type block struct {
	file           string
	l1, c1, l2, c2 int
	count          int
}

func readProfile(path string) ([]block, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []block
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		// file:l1.c1,l2.c2 stmts count
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) != 3 {
			continue
		}
		var b block
		b.file = line[:colon]
		if _, err := fmt.Sscanf(fields[0], "%d.%d,%d.%d", &b.l1, &b.c1, &b.l2, &b.c2); err != nil {
			continue
		}
		b.count, _ = strconv.Atoi(fields[2])
		out = append(out, b)
	}
	return out, sc.Err()
}

// Reached answers, for each site key, the suites whose profile executed the
// site's statement.
func Reached(sites []Site, profiles map[string]string) (map[string][]string, error) {
	out := map[string][]string{}
	names := make([]string, 0, len(profiles))
	for n := range profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		blocks, err := readProfile(profiles[name])
		if err != nil {
			return nil, err
		}
		byFile := map[string][]block{}
		for _, b := range blocks {
			base := filepath.Base(b.file)
			if strings.Contains(b.file, "/internal/irbuild/") && !strings.Contains(b.file[strings.Index(b.file, "/internal/irbuild/")+len("/internal/irbuild/"):], "/") {
				byFile[base] = append(byFile[base], b)
			}
		}
		for _, s := range sites {
			for _, b := range byFile[s.File] {
				if b.count > 0 && before(b.l1, b.c1, s.Line, s.Col) && before(s.Line, s.Col, b.l2, b.c2) {
					out[s.Key()] = append(out[s.Key()], name)
					break
				}
			}
		}
	}
	return out, nil
}

func before(l1, c1, l2, c2 int) bool {
	return l1 < l2 || (l1 == l2 && c1 <= c2)
}
