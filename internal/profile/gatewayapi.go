package profile

// gatewayAPI is the Gateway API release every recipe installs. Sharing one
// value lets G01 to G03 compare two controllers against the same CRDs, and
// gives the harness one tested combination (D30). A recipe that needs another
// release is a decision for its own ADR, not a local edit.
// TestEveryRecipeInstallsTheSameGatewayAPI fails the build if a recipe stops
// using this value.
var gatewayAPI = GatewayAPI{
	Version: "v1.6.1",
	Channel: "standard",
	SHA256:  "24d931f22abd8e40c973264319ead7cfa09d0fb7716b7ab1ee2ff174cb063a73",
}
