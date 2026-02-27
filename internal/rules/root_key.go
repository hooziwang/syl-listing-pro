package rules

import _ "embed"

//go:embed assets/rules_root_public.pem
var embeddedRootPublicKeyPEM []byte
