package vaultclient

import (
	"errors"
	"net/http"
	"time"

	"github.com/hashicorp/vault/api"
)

// Alias is copied from https://github.com/hashicorp/vault/blob/9fc412306dd8282aead42f77654aaaad71bf10e5/helper/identity/types.pb.go#L373
// change: timestamp.Timestamp replaced with time.Time
type Alias struct {
	// ID is the unique identifier that represents this alias
	ID string `sentinel:"" protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// CanonicalID is the entity identifier to which this alias belongs to
	CanonicalID string `sentinel:"" protobuf:"bytes,2,opt,name=canonical_id,json=canonicalId,proto3" json:"canonical_id,omitempty"`
	// MountType is the backend mount's type to which this alias belongs to.
	// This enables categorically querying aliases of specific backend types.
	MountType string `sentinel:"" protobuf:"bytes,3,opt,name=mount_type,json=mountType,proto3" json:"mount_type,omitempty"`
	// MountAccessor is the backend mount's accessor to which this alias
	// belongs to.
	MountAccessor string `sentinel:"" protobuf:"bytes,4,opt,name=mount_accessor,json=mountAccessor,proto3" json:"mount_accessor,omitempty"`
	// MountPath is the backend mount's path to which the Maccessor belongs to. This
	// field is not used for any operational purposes. This is only returned when
	// alias is read, only as a nicety.
	MountPath string `sentinel:"" protobuf:"bytes,5,opt,name=mount_path,json=mountPath,proto3" json:"mount_path,omitempty"`
	// Metadata is the explicit metadata that clients set against an entity
	// which enables virtual grouping of aliases. Aliases will be indexed
	// against their metadata.
	Metadata map[string]string `sentinel:"" protobuf:"bytes,6,rep,name=metadata,proto3" json:"metadata,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	// Name is the identifier of this alias in its authentication source.
	// This does not uniquely identify an alias in Vault. This in conjunction
	// with MountAccessor form to be the factors that represent an alias in a
	// unique way. Aliases will be indexed based on this combined uniqueness
	// factor.
	Name string `sentinel:"" protobuf:"bytes,7,opt,name=name,proto3" json:"name,omitempty"`
	// CreationTime is the time at which this alias was first created
	CreationTime *time.Time `sentinel:"" protobuf:"bytes,8,opt,name=creation_time,json=creationTime,proto3" json:"creation_time,omitempty"`
	// LastUpdateTime is the most recent time at which the properties of this
	// alias got modified. This is helpful in filtering out aliases based
	// on its age and to take action on them, if desired.
	LastUpdateTime *time.Time `sentinel:"" protobuf:"bytes,9,opt,name=last_update_time,json=lastUpdateTime,proto3" json:"last_update_time,omitempty"`
	// MergedFromCanonicalIDs is the FIFO history of merging activity
	MergedFromCanonicalIDs []string `sentinel:"" protobuf:"bytes,10,rep,name=merged_from_canonical_ids,json=mergedFromCanonicalIds,proto3" json:"merged_from_canonical_ids,omitempty"`
	// NamespaceID is the identifier of the namespace to which this alias
	// belongs.
	NamespaceID string `sentinel:"" protobuf:"bytes,11,opt,name=namespace_id,json=namespaceID,proto3" json:"namespace_id,omitempty"`
}

// Entity is copied from https://github.com/hashicorp/vault/blob/9fc412306dd8282aead42f77654aaaad71bf10e5/helper/identity/types.pb.go#L200
// Changes:
// * timestamp.Timestamp replaced with time.Time
// * GroupIDs field added
// * MFASecrets field removed, we don't use it and it has has a field of protobuf_oneof type which isn't really supported for json
type Entity struct {
	// Aliases are the identities that this entity is made of. This can be
	// empty as well to favor being able to create the entity first and then
	// incrementally adding aliases.
	Aliases []*Alias `sentinel:"" protobuf:"bytes,1,rep,name=aliases,proto3" json:"aliases,omitempty"`
	// ID is the unique identifier of the entity which always be a UUID. This
	// should never be allowed to be updated.
	ID string `sentinel:"" protobuf:"bytes,2,opt,name=id,proto3" json:"id,omitempty"`
	// Name is a unique identifier of the entity which is intended to be
	// human-friendly. The default name might not be human friendly since it
	// gets suffixed by a UUID, but it can optionally be updated, unlike the ID
	// field.
	Name string `sentinel:"" protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	// Metadata represents the explicit metadata which is set by the
	// clients.  This is useful to tie any information pertaining to the
	// aliases. This is a non-unique field of entity, meaning multiple
	// entities can have the same metadata set. Entities will be indexed based
	// on this explicit metadata. This enables virtual groupings of entities
	// based on its metadata.
	Metadata map[string]string `sentinel:"" protobuf:"bytes,4,rep,name=metadata,proto3" json:"metadata,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	// CreationTime is the time at which this entity is first created.
	CreationTime *time.Time `sentinel:"" protobuf:"bytes,5,opt,name=creation_time,json=creationTime,proto3" json:"creation_time,omitempty"`
	// LastUpdateTime is the most recent time at which the properties of this
	// entity got modified. This is helpful in filtering out entities based on
	// its age and to take action on them, if desired.
	LastUpdateTime *time.Time `sentinel:"" protobuf:"bytes,6,opt,name=last_update_time,json=lastUpdateTime,proto3" json:"last_update_time,omitempty"`
	// MergedEntityIDs are the entities which got merged to this one. Entities
	// will be indexed based on all the entities that got merged into it. This
	// helps to apply the actions on this entity on the tokens that are merged
	// to the merged entities. Merged entities will be deleted entirely and
	// this is the only trackable trail of its earlier presence.
	MergedEntityIDs []string `sentinel:"" protobuf:"bytes,7,rep,name=merged_entity_ids,json=mergedEntityIDs,proto3" json:"merged_entity_ids,omitempty"`
	// Policies the entity is entitled to
	Policies []string `sentinel:"" protobuf:"bytes,8,rep,name=policies,proto3" json:"policies,omitempty"`
	// BucketKey is the path of the storage packer key into which this entity is
	// stored.
	BucketKey string `sentinel:"" protobuf:"bytes,9,opt,name=bucket_key,json=bucketKey,proto3" json:"bucket_key,omitempty"`
	// MFASecrets holds the MFA secrets indexed by the identifier of the MFA
	// method configuration.
	// MFASecrets map[string]*mfa.Secret `sentinel:"" protobuf:"bytes,10,rep,name=mfa_secrets,json=mfaSecrets,proto3" json:"mfa_secrets,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	// Disabled indicates whether tokens associated with the account should not
	// be able to be used
	Disabled bool `sentinel:"" protobuf:"varint,11,opt,name=disabled,proto3" json:"disabled,omitempty"`
	// NamespaceID is the identifier of the namespace to which this entity
	// belongs to. Do not return this value over the API when reading the
	// entity.
	NamespaceID string `sentinel:"" protobuf:"bytes,12,opt,name=namespace_id,json=namespaceID,proto3" json:"namespace_id,omitempty"`

	// GroupIDs is added downstream. Upstream has no type that would include this: https://github.com/hashicorp/vault/blob/be65a227ef2e80f8588b3b13584b5c0d9238c1d7/vault/identity_store_entities.go#L407
	GroupIDs []string `json:"group_ids,omitempty"`
}

// Group represents an identity group. Copied from https://github.com/hashicorp/vault/blob/9fc412306dd8282aead42f77654aaaad71bf10e5/helper/identity/types.pb.go#L31
// Changes: timestamp.Timestamp replaced with time.Time
type Group struct {
	// ID is the unique identifier for this group
	ID string `sentinel:"" protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// Name is the unique name for this group
	Name string `sentinel:"" protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// Policies are the vault policies to be granted to members of this group
	Policies []string `sentinel:"" protobuf:"bytes,3,rep,name=policies,proto3" json:"policies,omitempty"`
	// ParentGroupIDs are the identifiers of those groups to which this group is a
	// member of. These will serve as references to the parent group in the
	// hierarchy.
	ParentGroupIDs []string `sentinel:"" protobuf:"bytes,4,rep,name=parent_group_ids,json=parentGroupIds,proto3" json:"parent_group_ids,omitempty"`
	// MemberEntityIDs are the identifiers of entities which are members of this
	// group
	MemberEntityIDs []string `sentinel:"" protobuf:"bytes,5,rep,name=member_entity_ids,json=memberEntityIDs,proto3" json:"member_entity_ids,omitempty"`
	// Metadata represents the custom data tied with this group
	Metadata map[string]string `sentinel:"" protobuf:"bytes,6,rep,name=metadata,proto3" json:"metadata,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	// CreationTime is the time at which this group was created
	CreationTime *time.Time `sentinel:"" protobuf:"bytes,7,opt,name=creation_time,json=creationTime,proto3" json:"creation_time,omitempty"`
	// LastUpdateTime is the time at which this group was last modified
	LastUpdateTime *time.Time `sentinel:"" protobuf:"bytes,8,opt,name=last_update_time,json=lastUpdateTime,proto3" json:"last_update_time,omitempty"`
	// ModifyIndex tracks the number of updates to the group. It is useful to detect
	// updates to the groups.
	ModifyIndex uint64 `sentinel:"" protobuf:"varint,9,opt,name=modify_index,json=modifyIndex,proto3" json:"modify_index,omitempty"`
	// BucketKey is the path of the storage packer key into which this group is
	// stored.
	BucketKey string `sentinel:"" protobuf:"bytes,10,opt,name=bucket_key,json=bucketKey,proto3" json:"bucket_key,omitempty"`
	// Alias is used to mark this group as an internal mapping of a group that
	// is external to the identity store. Alias can only be set if the 'type'
	// is set to 'external'.
	Alias *Alias `sentinel:"" protobuf:"bytes,11,opt,name=alias,proto3" json:"alias,omitempty"`
	// Type indicates if this group is an internal group or an external group.
	// Memberships of the internal groups can be managed over the API whereas
	// the memberships on the external group --for which a corresponding alias
	// will be set-- will be managed automatically.
	Type string `sentinel:"" protobuf:"bytes,12,opt,name=type,proto3" json:"type,omitempty"`
	// NamespaceID is the identifier of the namespace to which this group
	// belongs to. Do not return this value over the API when reading the
	// group.
	NamespaceID string `sentinel:"" protobuf:"bytes,13,opt,name=namespace_id,json=namespaceID,proto3" json:"namespace_id,omitempty"`
}

type keyResponse struct {
	Keys []string `json:"keys,omitempty"`
}

func IsNotFound(err error) bool {
	respErr := &api.ResponseError{}
	if ok := errors.As(err, &respErr); !ok {
		return false
	}
	return respErr.StatusCode == http.StatusNotFound
}

type aliasListData struct {
	KeyInfo map[string]aliasListEntry `json:"key_info,omitempty"`
}

type aliasListEntry struct {
	// CanonicalID is the ID of the underlying user object
	CanonicalID string `json:"canonical_id"`
	// The unique mount accessor for which this alias is valid
	MountAccessor string `json:"mount_accessor"`
	MountPath     string `json:"mount_path"`
	MountType     string `json:"mount_type"`
	Name          string `json:"name"`
}

// MountListResponse is a map mountPath -> mount properties
type MountListResponse map[string]MountOutput

// MountOutput is copied from https://github.com/hashicorp/vault/blob/be65a227ef2e80f8588b3b13584b5c0d9238c1d7/api/sys_mounts.go#L162
type MountOutput struct {
	UUID                  string            `json:"uuid"`
	Type                  string            `json:"type"`
	Description           string            `json:"description"`
	Accessor              string            `json:"accessor"`
	Config                MountConfigOutput `json:"config"`
	Options               map[string]string `json:"options"`
	Local                 bool              `json:"local"`
	SealWrap              bool              `json:"seal_wrap" mapstructure:"seal_wrap"`
	ExternalEntropyAccess bool              `json:"external_entropy_access" mapstructure:"external_entropy_access"`
}

// MountConfigOutput is copied from https://github.com/hashicorp/vault/blob/be65a227ef2e80f8588b3b13584b5c0d9238c1d7/api/sys_mounts.go#L174
type MountConfigOutput struct {
	DefaultLeaseTTL           int      `json:"default_lease_ttl" mapstructure:"default_lease_ttl"`
	MaxLeaseTTL               int      `json:"max_lease_ttl" mapstructure:"max_lease_ttl"`
	ForceNoCache              bool     `json:"force_no_cache" mapstructure:"force_no_cache"`
	AuditNonHMACRequestKeys   []string `json:"audit_non_hmac_request_keys,omitempty" mapstructure:"audit_non_hmac_request_keys"`
	AuditNonHMACResponseKeys  []string `json:"audit_non_hmac_response_keys,omitempty" mapstructure:"audit_non_hmac_response_keys"`
	ListingVisibility         string   `json:"listing_visibility,omitempty" mapstructure:"listing_visibility"`
	PassthroughRequestHeaders []string `json:"passthrough_request_headers,omitempty" mapstructure:"passthrough_request_headers"`
	AllowedResponseHeaders    []string `json:"allowed_response_headers,omitempty" mapstructure:"allowed_response_headers"`
	TokenType                 string   `json:"token_type,omitempty" mapstructure:"token_type"`

	// Deprecated: This field will always be blank for newer server responses.
	PluginName string `json:"plugin_name,omitempty" mapstructure:"plugin_name"`
}

type KVData struct {
	Data     map[string]string `json:"data"`
	Metadata KVMetadata        `json:"metadata"`
}

type KVMetadata struct {
	CreatedTime time.Time `json:"created_time"`
	Destroyed   bool      `json:"destroyed,omitempty"`
	Version     int       `json:"version"`
}
