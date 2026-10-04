package rt

// EnterScopedField installs an immutable override inherited by callees and tasks.
func EnterScopedField[T any](parent *Frame, name string, value T) *Frame {
	child := *parent
	child.scopedFields = make(map[string]any, len(parent.scopedFields)+1)
	for key, v := range parent.scopedFields {
		child.scopedFields[key] = v
	}
	child.scopedFields[name] = value
	return &child
}

// PublishScoped installs a complete boot schema. The caller relinquishes fields.
func PublishScoped(fr *Frame, fields map[string]any) {
	fr.scopedFields = fields
	fr.booted = fr.booted.withFields(fields)
}

// bootedApp is boot's published application: its fields and its context. It
// is immutable, and each publication replaces the frame's pointer, so a frame
// that inherited an earlier publication (a parent test boot's) keeps it.
type bootedApp struct {
	fields  map[string]any
	context *Context
}

func (b *bootedApp) withFields(fields map[string]any) *bootedApp {
	next := bootedApp{fields: fields}
	if b != nil {
		next.context = b.context
	}
	return &next
}

func (b *bootedApp) withContext(context *Context) *bootedApp {
	next := bootedApp{context: context}
	if b != nil {
		next.fields = b.fields
	}
	return &next
}

// LookupScopedField answers one application field from the calling lineage,
// and whether the lineage publishes it, for a host that reports its own
// failure rather than trapping.
func LookupScopedField(fr *Frame, name string) (any, bool) {
	if fr == nil {
		return nil, false
	}
	v, ok := fr.scopedFields[name]
	return v, ok
}
