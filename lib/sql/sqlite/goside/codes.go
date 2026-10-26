package goside

import "strconv"

// SQLite's result codes, as sqlite3.h numbers them. Both builds read them:
// the native one gets them from modernc.org/sqlite, and the browser one from
// SQLite's official WebAssembly build, and SQLite numbers them the same way
// in each.
const (
	sqliteOK         = 0
	sqliteError      = 1
	sqliteInternal   = 2
	sqlitePerm       = 3
	sqliteAbort      = 4
	sqliteBusy       = 5
	sqliteLocked     = 6
	sqliteNomem      = 7
	sqliteReadonly   = 8
	sqliteInterrupt  = 9
	sqliteIOErr      = 10
	sqliteCorrupt    = 11
	sqliteFull       = 13
	sqliteCantOpen   = 14
	sqliteSchema     = 17
	sqliteTooBig     = 18
	sqliteConstraint = 19
	sqliteMismatch   = 20
	sqliteMisuse     = 21
	sqliteRange      = 25
	sqliteNotADB     = 26
	sqliteRow        = 100
	sqliteDone       = 101

	sqliteConstraintCheck      = sqliteConstraint | 1<<8
	sqliteConstraintForeignKey = sqliteConstraint | 3<<8
	sqliteConstraintNotNull    = sqliteConstraint | 5<<8
	sqliteConstraintPrimaryKey = sqliteConstraint | 6<<8
	sqliteConstraintUnique     = sqliteConstraint | 8<<8
)

// codeName is SQLite's name for a result code, SQLITE_CONSTRAINT_UNIQUE
// for 2067, or the number when it has none here.
func codeName(code int) string {
	if name, ok := codeNames[code]; ok {
		return name
	}
	if name, ok := codeNames[code&0xff]; ok {
		return name
	}
	return strconv.Itoa(code)
}

var codeNames = map[int]string{
	sqliteError:                "SQLITE_ERROR",
	sqliteInternal:             "SQLITE_INTERNAL",
	sqlitePerm:                 "SQLITE_PERM",
	sqliteAbort:                "SQLITE_ABORT",
	sqliteBusy:                 "SQLITE_BUSY",
	sqliteLocked:               "SQLITE_LOCKED",
	sqliteNomem:                "SQLITE_NOMEM",
	sqliteReadonly:             "SQLITE_READONLY",
	sqliteInterrupt:            "SQLITE_INTERRUPT",
	sqliteIOErr:                "SQLITE_IOERR",
	sqliteCorrupt:              "SQLITE_CORRUPT",
	sqliteFull:                 "SQLITE_FULL",
	sqliteCantOpen:             "SQLITE_CANTOPEN",
	sqliteSchema:               "SQLITE_SCHEMA",
	sqliteTooBig:               "SQLITE_TOOBIG",
	sqliteConstraint:           "SQLITE_CONSTRAINT",
	sqliteMismatch:             "SQLITE_MISMATCH",
	sqliteMisuse:               "SQLITE_MISUSE",
	sqliteRange:                "SQLITE_RANGE",
	sqliteNotADB:               "SQLITE_NOTADB",
	sqliteConstraintCheck:      "SQLITE_CONSTRAINT_CHECK",
	sqliteConstraintForeignKey: "SQLITE_CONSTRAINT_FOREIGNKEY",
	sqliteConstraintNotNull:    "SQLITE_CONSTRAINT_NOTNULL",
	sqliteConstraintPrimaryKey: "SQLITE_CONSTRAINT_PRIMARYKEY",
	sqliteConstraintUnique:     "SQLITE_CONSTRAINT_UNIQUE",
}
