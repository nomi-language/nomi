module qualified_alias_app

go 1.27.0

require (
	qualified_alias_dep v0.0.0
	qualified_alias_src v0.0.0
)

replace qualified_alias_dep => ../qualified_alias_dep

replace qualified_alias_src => ../qualified_alias_src
