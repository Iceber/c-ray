 Package metadata stores all labels and object specific metadata by namespace.
 This package also contains the main garbage collection logic for cleaning up
 resources consistently and atomically. Resources used by backends will be
 tracked in the metadata store to be exposed to consumers of this package.

 The layout where a "/" delineates a bucket is described in the following
 section. Please try to follow this as closely as possible when adding
 functionality. We can bolster this with helpers and more structure if that
 becomes an issue.

 Generically, we try to do the following:

	<version>/<namespace>/<object>/<key> -> <field>

	version
	           Currently, this is "v1". Additions can be made to v1 in a backwards
	           compatible way. If the layout changes, a new version must be made,
	           along with a migration.

	namespace
	           The namespace to which this object belongs.

	object
	           Defines which object set is stored in the bucket. There are two
	           special objects, "labels" and "indexes". The "labels" bucket
	           stores the labels for the parent namespace. The "indexes" object
	           is reserved for indexing objects, if we require in the future.

	key
	           Object-specific key identifying the storage bucket for the
	           object's contents.

 Below is the current database schema. This should be updated each time
 the structure is changed in addition to adding a migration and incrementing
 the database version.

	Notes
	   • `╘══*...*` refers to maps with arbitrary keys
	   • `version` is a key to a numeric value identifying the minor revisions
	     of schema version
	   • a namespace in a schema bucket cannot be named "version"

	Schema
	└──v1                                             - Schema version bucket
	   ├──version : <varint>                          - Latest version, see migrations
	   ╘══*namespace*
	      ├──labels
	      │  ╘══*key* : <string>                      - Label value
	      ├──image
	      │  ╘══*image name*
	      │     ├──createdat : <binary time>          - Created at
	      │     ├──updatedat : <binary time>          - Updated at
	      │     ├──target
	      │     │  ├──digest : <digest>               - Descriptor digest
	      │     │  ├──mediatype : <string>            - Descriptor media type
	      │     │  └──size : <varint>                 - Descriptor size
	      │     └──labels
	      │        ╘══*key* : <string>                - Label value
	      ├──containers
	      │  ╘══*container id*
	      │     ├──createdat : <binary time>          - Created at
	      │     ├──updatedat : <binary time>          - Updated at
	      │     ├──spec : <binary>                    - Proto marshaled spec
	      │     ├──image : <string>                   - Image name
	      │     ├──snapshotter : <string>             - Snapshotter name
	      │     ├──snapshotKey : <string>             - Snapshot key
	      │     ├──runtime
	      │     │  ├──name : <string>                 - Runtime name
	      │     │  └──options : <binary>              - Proto marshaled options
	      │     ├──extensions
	      │     │     ╘══*name* : <binary>            - Proto marshaled extension
	      │     └──labels
	      │        ╘══*key* : <string>                - Label value
	      ├──snapshots
	      │  ╘══*snapshotter*
	      │     ╘══*snapshot key*
	      │        ├──name : <string>                 - Snapshot name in backend
	      │        ├──createdat : <binary time>       - Created at
	      │        ├──updatedat : <binary time>       - Updated at
	      │        ├──parent : <string>               - Parent snapshot name
	      │        ├──children
	      │        │  ╘══*snapshot key* : <nil>       - Child snapshot reference
	      │        └──labels
	      │           ╘══*key* : <string>             - Label value
	      ├──content
	      │  ├──blob
	      │  │  ╘══*blob digest*
	      │  │     ├──createdat : <binary time>       - Created at
	      │  │     ├──updatedat : <binary time>       - Updated at
	      │  │     ├──size : <varint>                 - Blob size
	      │  │     └──labels
	      │  │        ╘══*key* : <string>             - Label value
	      │  └──ingests
	      │     ╘══*ingest reference*
	      │        ├──ref : <string>                  - Ingest reference in backend
	      │        ├──expireat : <binary time>        - Time to expire ingest
	      │        └──expected : <digest>             - Expected commit digest
	      ├──sandboxes
	      │  ╘══*sandbox id*
	      │     ├──createdat : <binary time>          - Created at
	      │     ├──updatedat : <binary time>          - Updated at
	      │     ├──spec : <binary>                    - Proto marshaled spec
	      │     ├──sandboxer : <string>               - Sandboxer name
	      │     ├──runtime
	      │     │  ├──name : <string>                 - Runtime name
	      │     │  └──options : <binary>              - Proto marshaled options
	      │     ├──extensions
	      │     │  ╘══*name* : <binary>               - Proto marshaled extension
	      │     └──labels
	      │        ╘══*key* : <string>                - Label value
	      └──leases
	         ╘══*lease id*
	             ├──createdat : <binary time>         - Created at
	             ├──labels
	             │  ╘══*key* : <string>               - Label value
	             ├──snapshots
	             │  ╘══*snapshotter*
	             │     ╘══*snapshot key* : <nil>      - Snapshot reference
	             ├──content
	             │  ╘══*blob digest* : <nil>          - Content blob reference
	             └─────ingests
	                   ╘══*ingest reference* : <nil> - Content ingest reference
