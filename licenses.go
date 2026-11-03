// Package looster retains redistribution notices in every distributed binary.
package looster

import _ "embed"

// License is the license of Looster's original code.
//
//go:embed LICENSE
var License string

// ThirdPartyNotices retains dependency license texts and source availability.
//
//go:embed THIRD_PARTY_NOTICES.md
var ThirdPartyNotices string
