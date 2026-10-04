module orphan_violator

go 1.27.0

require orphan_dep v0.0.0

replace orphan_dep => ../orphan_dep
