// Import all infrastructure and security plugin packages to trigger their init() registration.
package plugins

import (
	_ "github.com/alphabravocompany/thewolf/plugins/additional"
	_ "github.com/alphabravocompany/thewolf/plugins/container"
	_ "github.com/alphabravocompany/thewolf/plugins/docs"
	_ "github.com/alphabravocompany/thewolf/plugins/general"
	_ "github.com/alphabravocompany/thewolf/plugins/infra"
	_ "github.com/alphabravocompany/thewolf/plugins/sbom"
	_ "github.com/alphabravocompany/thewolf/plugins/security"
)
