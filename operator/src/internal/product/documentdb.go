// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package product

import (
	"os"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// DocumentDBProfile returns the ProductProfile for the DocumentDB product. Every
// field is sourced from the canonical operator constants so this profile stays
// the single mapping between product identity and operator defaults.
func DocumentDBProfile() ProductProfile {
	return ProductProfile{
		Name:                    "DocumentDB",
		ExtensionImageRepo:      util.ExtensionImageRepo(),
		GatewayImageRepo:        util.GatewayImageRepo(),
		DefaultTag:              util.DEFAULT_DOCUMENTDB_TAG,
		DefaultCredentialSecret: util.DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET,
		SidecarInjectorPlugin:   util.DEFAULT_SIDECAR_INJECTOR_PLUGIN,
		WALReplicaPlugin:        util.DEFAULT_WAL_REPLICA_PLUGIN,
		Postgres: PostgresProfile{
			Extension: ExtensionSpec{
				Name:                 DocumentDBExtensionName,
				DynamicLibraryPath:   []string{"lib"},
				ExtensionControlPath: []string{"share"},
				LdLibraryPath:        []string{"lib", "system"},
			},
			AdditionalLibraries: []string{"pg_cron", "pg_documentdb_core", "pg_documentdb"},
			BootstrapSQL: []string{
				"CREATE EXTENSION documentdb CASCADE",
				"CREATE ROLE documentdb WITH LOGIN PASSWORD 'Admin100'",
				"ALTER ROLE documentdb WITH SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS",
			},
			PgHBA: []string{
				"host all all localhost trust",
				"hostssl replication streaming_replica all cert",
			},
			ProtectedParameters: map[string]string{
				"cron.database_name": "postgres",
			},
			ReplicationAdminRole: "documentdb_admin_role",
		},
		Naming: NamingProfile{
			Domain:                 "documentdb.io",
			ServicePrefix:          util.DOCUMENTDB_SERVICE_PREFIX,
			MetricClusterAttribute: "documentdb.cluster",
			HealthMetricName:       "documentdb.postgres.up",
			OperatorName:           "documentdb-operator",
			Kind:                   "DocumentDB",
		},
	}
}

// DocumentDBExtensionName is the PostgreSQL extension backing DocumentDB.
const DocumentDBExtensionName = "documentdb"

// DocumentDBAdapter is the first product adapter. It maps the DocumentDB custom
// resource onto the product-neutral model consumed by the reconciler.
type DocumentDBAdapter struct{}

// Profile returns the DocumentDB product profile.
func (DocumentDBAdapter) Profile() ProductProfile {
	return DocumentDBProfile()
}

// ExtensionImage resolves the extension image for a DocumentDB instance using the
// product profile's repo and default. The resolution priority is shared across
// products via util.ResolveComponentImage.
func (a DocumentDBAdapter) ExtensionImage(db *dbpreview.DocumentDB) string {
	p := a.Profile()
	var explicit string
	if db.Spec.Image != nil {
		explicit = db.Spec.Image.DocumentDB
	}
	return util.ResolveComponentImage(
		p.ExtensionImageRepo,
		p.DefaultTag,
		explicit,
		db.Spec.DocumentDBVersion,
		os.Getenv(util.DOCUMENTDB_VERSION_ENV),
		util.CHANGESTREAM_DOCUMENTDB_IMAGE,
		dbpreview.IsFeatureGateEnabled(db, dbpreview.FeatureGateChangeStreams),
	)
}

// GatewayImage resolves the gateway image for a DocumentDB instance using the
// product profile's repo and default.
func (a DocumentDBAdapter) GatewayImage(db *dbpreview.DocumentDB) string {
	p := a.Profile()
	var explicit string
	if db.Spec.Image != nil {
		explicit = db.Spec.Image.Gateway
	}
	return util.ResolveComponentImage(
		p.GatewayImageRepo,
		p.DefaultTag,
		explicit,
		db.Spec.DocumentDBVersion,
		os.Getenv(util.DOCUMENTDB_VERSION_ENV),
		util.CHANGESTREAM_GATEWAY_IMAGE,
		dbpreview.IsFeatureGateEnabled(db, dbpreview.FeatureGateChangeStreams),
	)
}

// DocumentDBResources maps spec.resource of a DocumentDB onto the neutral
// resource model consumed by the resource split and validation.
func DocumentDBResources(db *dbpreview.DocumentDB) Resources {
	res := db.Spec.Resource
	return Resources{
		Memory:   res.Memory,
		CPU:      res.CPU,
		Database: toComponentResources(res.Database),
		Gateway:  toComponentResources(res.Gateway),
		OTel:     toComponentResources(res.OTel),
	}
}

func toComponentResources(c *dbpreview.ComponentResources) *ComponentResources {
	if c == nil {
		return nil
	}
	return &ComponentResources{Memory: c.Memory, CPU: c.CPU}
}

// ToClusterIntent maps a DocumentDB custom resource onto the product-neutral
// ClusterIntent, resolving product-varying images, credential secret, and plugin
// names via the product profile.
func (a DocumentDBAdapter) ToClusterIntent(db *dbpreview.DocumentDB) ClusterIntent {
	p := a.Profile()

	credentialSecret := db.Spec.DocumentDbCredentialSecret
	if credentialSecret == "" {
		credentialSecret = p.DefaultCredentialSecret
	}

	sidecarPlugin := p.SidecarInjectorPlugin
	if db.Spec.Plugins != nil && db.Spec.Plugins.SidecarInjectorName != "" {
		sidecarPlugin = db.Spec.Plugins.SidecarInjectorName
	}

	var postgresImage string
	if db.Spec.Image != nil && db.Spec.Image.Postgres != "" {
		// A CR-pinned operand image wins.
		postgresImage = db.Spec.Image.Postgres
	} else {
		// Otherwise use the operator-level default (POSTGRES_IMAGE env). When that
		// is empty the field stays empty and CloudNativePG applies its built-in
		// operand default.
		postgresImage = util.PostgresImage()
	}

	pg := Postgres{
		LogLevel:  db.Spec.LogLevel,
		StopDelay: db.Spec.Timeouts.StopDelay,
	}
	if db.Spec.Postgres != nil {
		pg.UID = db.Spec.Postgres.UID
		pg.GID = db.Spec.Postgres.GID
		pg.PostInitSQL = db.Spec.Postgres.PostInitSQL
		pg.Parameters = db.Spec.Postgres.Parameters
	}

	var tls TLS
	if db.Status.TLS != nil && db.Status.TLS.Ready {
		tls.GatewaySecret = db.Status.TLS.SecretName
	}
	if db.Spec.TLS != nil {
		tls.Postgres = db.Spec.TLS.Postgres
	}

	var bootstrap Bootstrap
	if db.Spec.Bootstrap != nil && db.Spec.Bootstrap.Recovery != nil {
		recovery := db.Spec.Bootstrap.Recovery
		r := Recovery{BackupName: recovery.Backup.Name}
		if recovery.PersistentVolume != nil {
			r.PersistentVolumeName = recovery.PersistentVolume.Name
		}
		bootstrap.Recovery = &r
	}

	return ClusterIntent{
		Images: Images{
			PostgresExtension:           a.ExtensionImage(db),
			PostgresExtensionPullPolicy: ParsePullPolicy(os.Getenv(util.DOCUMENTDB_IMAGE_PULL_POLICY_ENV)),
			Gateway:                     a.GatewayImage(db),
			GatewayPullPolicy:           os.Getenv(util.GATEWAY_IMAGE_PULL_POLICY_ENV),
			Postgres:                    postgresImage,
			PullSecrets:                 db.Spec.ImagePullSecrets,
		},
		Topology: Topology{
			Instances: db.Spec.InstancesPerNode,
			Affinity:  db.Spec.Affinity,
		},
		Storage: Storage{
			PvcSize: db.Spec.Resource.Storage.PvcSize,
		},
		Resources:  DocumentDBResources(db),
		Monitoring: db.Spec.Monitoring,
		TLS:        tls,
		Identity: Identity{
			Name:       db.Name,
			UID:        db.UID,
			APIVersion: db.APIVersion,
			Kind:       db.Kind,
		},
		Postgres: pg,
		FeatureGates: FeatureGates{
			ChangeStreams: dbpreview.IsFeatureGateEnabled(db, dbpreview.FeatureGateChangeStreams),
			IOUring:       dbpreview.IsFeatureGateEnabled(db, dbpreview.FeatureGateIOUring),
		},
		Bootstrap:             bootstrap,
		CredentialSecret:      credentialSecret,
		SidecarInjectorPlugin: sidecarPlugin,
		WALReplicaPlugin:      p.WALReplicaPlugin,
		Product:               p,
	}
}

// compile-time assertion that DocumentDBAdapter satisfies the Adapter seam.
var _ Adapter = DocumentDBAdapter{}

// DocumentDBAdapter implements the full shared-reconciler contract.
var _ ClusterAdapter[*dbpreview.DocumentDB] = DocumentDBAdapter{}

// New returns an empty DocumentDB custom resource.
func (DocumentDBAdapter) New() *dbpreview.DocumentDB { return &dbpreview.DocumentDB{} }

// Intent is ToClusterIntent under the ClusterAdapter contract.
func (a DocumentDBAdapter) Intent(db *dbpreview.DocumentDB) ClusterIntent {
	return a.ToClusterIntent(db)
}

// Spec returns the operational view of a DocumentDB custom resource.
func (DocumentDBAdapter) Spec(db *dbpreview.DocumentDB) ClusterSpec {
	spec := ClusterSpec{
		ServiceType:                   db.Spec.ExposeViaService.ServiceType,
		Environment:                   db.Spec.Environment,
		StorageClass:                  db.Spec.Resource.Storage.StorageClass,
		PersistentVolumeReclaimPolicy: db.Spec.Resource.Storage.PersistentVolumeReclaimPolicy,
		RecoveryPersistentVolume:      db.GetPVNameForRecovery(),
		SchemaVersion:                 db.Spec.SchemaVersion,
		Monitoring:                    db.Spec.Monitoring,
		Backup:                        db.Spec.Backup,
	}
	if db.Spec.TLS != nil {
		spec.GatewayTLS = db.Spec.TLS.Gateway
	}
	return spec
}

// Status reads the neutral status fields from a DocumentDB custom resource.
func (DocumentDBAdapter) Status(db *dbpreview.DocumentDB) ClusterStatus {
	return ClusterStatus{
		Phase:            db.Status.Status,
		ConnectionString: db.Status.ConnectionString,
		SchemaVersion:    db.Status.SchemaVersion,
		ExtensionImage:   db.Status.DocumentDBImage,
		GatewayImage:     db.Status.GatewayImage,
		TLS:              db.Status.TLS,
	}
}

// SetStatus writes the neutral status fields into a DocumentDB custom resource;
// multi-region fields (TargetPrimary, LocalPrimary) are left untouched.
func (DocumentDBAdapter) SetStatus(db *dbpreview.DocumentDB, s ClusterStatus) {
	db.Status.Status = s.Phase
	db.Status.ConnectionString = s.ConnectionString
	db.Status.SchemaVersion = s.SchemaVersion
	db.Status.DocumentDBImage = s.ExtensionImage
	db.Status.GatewayImage = s.GatewayImage
	db.Status.TLS = s.TLS
}

// ConnectionString builds the MongoDB-protocol connection string for a
// DocumentDB gateway.
func (a DocumentDBAdapter) ConnectionString(db *dbpreview.DocumentDB, host string, port int32, trustTLS bool) string {
	secretName := db.Spec.DocumentDbCredentialSecret
	if secretName == "" {
		secretName = a.Profile().DefaultCredentialSecret
	}
	return util.MongoConnectionString(secretName, db.Namespace, host, port, trustTLS)
}
