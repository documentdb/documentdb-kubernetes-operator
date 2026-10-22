// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package product defines the product-neutral seam the operator reconciles
// against. A ProductProfile captures the identity, image defaults, and CNPG
// plugin/secret conventions that vary between operator products, and an Adapter
// translates a product-specific custom resource into that neutral model. The
// reconciler consumes the profile and a ClusterIntent, so it carries no
// product-branding logic; each product supplies its own adapter without changing
// the reconciler.
package product

// ProductProfile captures the product-varying identity and defaults for an
// operator product. Values are sourced from the canonical operator constants so
// existing behavior is preserved exactly.
type ProductProfile struct {
	// Name is the human-readable product name.
	Name string

	// ExtensionImageRepo and GatewayImageRepo are the image repositories used
	// when resolving an image from a bare version string.
	ExtensionImageRepo string
	GatewayImageRepo   string

	// DefaultTag is the image tag used when neither an explicit image nor a
	// version is supplied. It is composed onto the (possibly overridden)
	// repository so a registry override flows through to the default image.
	DefaultTag string

	// DefaultCredentialSecret is the credential secret name used when the custom
	// resource does not specify one.
	DefaultCredentialSecret string

	// SidecarInjectorPlugin and WALReplicaPlugin are the CNPG plugin names the
	// operator wires onto the rendered Cluster.
	SidecarInjectorPlugin string
	WALReplicaPlugin      string

	// Postgres is the product's PostgreSQL contract: extension, libraries,
	// bootstrap SQL, host-based auth, and operator-forced GUCs.
	Postgres PostgresProfile

	// Gateway is the product's gateway sidecar contract. Zero values keep the
	// sidecar injector's DocumentDB gateway defaults.
	Gateway GatewayProfile

	// Naming is the product's label/annotation/finalizer domain and resource
	// naming conventions used by the shared reconcilers.
	Naming NamingProfile
}

// GatewayProfile describes how the sidecar injector runs the product gateway.
// Every field is optional; an empty value is not sent to the plugin so the
// injector falls back to its DocumentDB gateway defaults.
type GatewayProfile struct {
	// ContainerName is the injected gateway container name.
	ContainerName string
	// Port is the gateway container port.
	Port int32
	// Args, when non-nil, replaces the DocumentDB gateway arguments verbatim.
	Args []string
	// UsernameEnv and PasswordEnv are the env var names the credential
	// secret's username/password keys are exposed as.
	UsernameEnv string
	PasswordEnv string
}

// ExtensionSpec describes the product's PostgreSQL extension, mounted from the
// extension image through CNPG's ImageVolume extension support.
type ExtensionSpec struct {
	// Name is the CNPG extension entry name and the PostgreSQL extension name
	// used for CREATE/ALTER EXTENSION and version discovery. It must satisfy
	// CNPG's extension-name constraints (1-59 characters, lowercase letters,
	// digits, internal hyphens/underscores, and alphanumeric ends).
	Name string
	// DynamicLibraryPath, ExtensionControlPath, and LdLibraryPath are the
	// image-relative paths CNPG adds to the PostgreSQL search paths.
	DynamicLibraryPath   []string
	ExtensionControlPath []string
	LdLibraryPath        []string
}

// PostgresProfile captures the product-wide PostgreSQL contract.
type PostgresProfile struct {
	// Extension is the product extension mounted from the extension image.
	Extension ExtensionSpec
	// AdditionalLibraries are added to shared_preload_libraries.
	AdditionalLibraries []string
	// BootstrapSQL runs once at initdb, before any user PostInitSQL.
	BootstrapSQL []string
	// PgHBA is the host-based authentication configuration.
	PgHBA []string
	// ProtectedParameters are product GUCs the operator always forces, on top
	// of the core protected set.
	ProtectedParameters map[string]string
	// ReplicationAdminRole, when set, is granted to CNPG's streaming_replica
	// user so physical replicas can read product-owned objects.
	ReplicationAdminRole string
}

// Adapter is the product-neutral contract shared by every product integration:
// it exposes the product's static Profile. Each concrete adapter (for example
// DocumentDBAdapter) also provides a typed ToClusterIntent for its own custom
// resource; that method is not on this interface because the custom-resource
// type is product-specific.
type Adapter interface {
	// Profile returns the static product profile.
	Profile() ProductProfile
}
