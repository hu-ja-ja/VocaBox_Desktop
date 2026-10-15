// Package vocabox carries what the app says about the licences of the works
// it bundles: its own licence, and the entries of the settings list, which
// tools/notices writes into notices_gen.go.
package vocabox

import _ "embed"

//go:generate go run ./tools/notices

// AppLicense is VocaBox's own licence, the Mozilla Public License 2.0.
//
//go:embed LICENSE
var AppLicense string
