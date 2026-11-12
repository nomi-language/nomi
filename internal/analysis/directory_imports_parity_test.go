package analysis_test

import "testing"

// directoryImportsProject reaches files through a directory import: main
// writes `admin::users` and, after an edit, `admin::reports::daily`, and
// nothing else under views/admin is part of the program until it does.
func directoryImportsProject() incrementalProject {
	return incrementalProject{name: "directory imports", entry: "main", texts: map[string]string{
		"main": `import
    std::io
    views::admin
end

fn show(detail: admin::users.UserDetail) -> String
    admin::users.render(detail)
end

fn main()
    io.print(show(admin::users.UserDetail{name: "ada"}))
end
`,
		"views/admin/users": `pub struct UserDetail
    name: String
end

pub fn render(detail: UserDetail) -> String
    "<${detail.name}>"
end
`,
		"views/admin/reports/daily": `pub fn total() -> Int
    3
end
`,
	}}
}

// directoryImportsEdits add and remove the paths main writes below the
// directory's name, and edit a file it reaches.
var directoryImportsEdits = []editStep{
	replace("body only", "main", `name: "ada"`, `name: "grace"`),
	restore("body back", "main"),
	replace("a body reaches another file", "main", "    io.print(show(", "    io.print(admin::reports::daily.total())\n    io.print(show("),
	restore("the path removed", "main"),
	replace("a path that reaches no file", "main", "admin::users.render(detail)", "admin::user.render(detail)"),
	restore("the path back", "main"),
	replace("a reached file's signature", "views/admin/users", "pub fn render(detail: UserDetail) -> String", "pub fn render(detail: UserDetail, width: Int) -> String"),
	restore("the signature back", "views/admin/users"),
	replace("body only in a reached file", "views/admin/users", `"<${detail.name}>"`, `detail.name`),
	replace("every path removed", "main", "fn show(detail: admin::users.UserDetail) -> String\n    admin::users.render(detail)\nend\n", "fn show(detail: String) -> String\n    detail\nend\n"),
}

// TestDirectoryImportsIncrementalAndFromInterfaces holds the files a
// directory import reaches to the rules a file import's file is held to: an
// incremental recheck answers what a full check does, and so does a check of
// each file against interfaces.
func TestDirectoryImportsIncrementalAndFromInterfaces(t *testing.T) {
	p := directoryImportsProject()
	if errs := fullCheck(p, p.texts).Errors; len(errs["main"]) > 0 {
		t.Fatalf("the project does not check: %v", errs["main"])
	}
	t.Run("incremental", func(t *testing.T) {
		runChecked(t, p, directoryImportsEdits, newManager().RecheckWithOutcome)
	})
	t.Run("from interfaces", func(t *testing.T) {
		interfaceSteps(t, p, directoryImportsEdits)
	})
}
