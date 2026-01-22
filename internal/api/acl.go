package api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// AccessControlPolicy represents the XML structure for ACL.
type AccessControlPolicy struct {
	XMLName           xml.Name          `xml:"AccessControlPolicy"`
	Xmlns             string            `xml:"xmlns,attr,omitempty"`
	Owner             Owner             `xml:"Owner"`
	AccessControlList AccessControlList `xml:"AccessControlList"`
}

// AccessControlList represents the list of grants.
type AccessControlList struct {
	Grants []Grant `xml:"Grant"`
}

// Grant represents a single grant in an ACL.
type Grant struct {
	Grantee    Grantee `xml:"Grantee"`
	Permission string  `xml:"Permission"`
}

// Grantee represents who is granted access.
type Grantee struct {
	XMLName     xml.Name `xml:"Grantee"`
	XsiType     string   `xml:"http://www.w3.org/2001/XMLSchema-instance type,attr"`
	ID          string   `xml:"ID,omitempty"`
	DisplayName string   `xml:"DisplayName,omitempty"`
	URI         string   `xml:"URI,omitempty"`
}

// validCannedACLs contains all valid canned ACL values.
var validCannedACLs = map[string]bool{
	"private":                   true,
	"public-read":               true,
	"public-read-write":         true,
	"authenticated-read":        true,
	"bucket-owner-read":         true,
	"bucket-owner-full-control": true,
}

// isValidCannedACL checks if the given ACL string is a valid canned ACL.
func isValidCannedACL(acl string) bool {
	return validCannedACLs[acl]
}

// GetBucketAcl handles GET /{bucket}?acl - GetBucketAcl.
func (h *Handler) GetBucketAcl(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	acl, err := h.storage.GetBucketACL(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	response := storageACLToXML(acl)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetBucketAcl response")
	}
}

// PutBucketAcl handles PUT /{bucket}?acl - PutBucketAcl.
func (h *Handler) PutBucketAcl(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Check for canned ACL header
	cannedACL := r.Header.Get("x-amz-acl")
	if cannedACL != "" {
		if !isValidCannedACL(cannedACL) {
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket)
			return
		}
		acl := storage.CannedACLToACL(storage.CannedACL(cannedACL), storage.DefaultOwnerID, storage.DefaultOwnerDisplay)
		if err := h.storage.PutBucketACL(r.Context(), bucket, acl); err != nil {
			if errors.Is(err, storage.ErrBucketNotFound) {
				WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
				return
			}
			WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request body for explicit ACL
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket)
		return
	}

	if len(body) > 0 {
		var aclPolicy AccessControlPolicy
		if err := xml.Unmarshal(body, &aclPolicy); err != nil {
			WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket)
			return
		}

		acl := xmlACLToStorage(&aclPolicy)
		if err := h.storage.PutBucketACL(r.Context(), bucket, acl); err != nil {
			if errors.Is(err, storage.ErrBucketNotFound) {
				WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
				return
			}
			WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

// GetObjectAcl handles GET /{bucket}/{key}?acl - GetObjectAcl.
func (h *Handler) GetObjectAcl(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	acl, err := h.storage.GetObjectACL(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
		return
	}

	response := storageACLToXML(acl)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetObjectAcl response")
	}
}

// PutObjectAcl handles PUT /{bucket}/{key}?acl - PutObjectAcl.
func (h *Handler) PutObjectAcl(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	// Check for canned ACL header
	cannedACL := r.Header.Get("x-amz-acl")
	if cannedACL != "" {
		if !isValidCannedACL(cannedACL) {
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
			return
		}
		acl := storage.CannedACLToACL(storage.CannedACL(cannedACL), storage.DefaultOwnerID, storage.DefaultOwnerDisplay)
		if err := h.storage.PutObjectACL(r.Context(), bucket, key, acl); err != nil {
			if errors.Is(err, storage.ErrBucketNotFound) {
				WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
				return
			}
			if errors.Is(err, storage.ErrObjectNotFound) {
				WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
				return
			}
			WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request body for explicit ACL
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket+"/"+key)
		return
	}

	if len(body) > 0 {
		var aclPolicy AccessControlPolicy
		if err := xml.Unmarshal(body, &aclPolicy); err != nil {
			WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket+"/"+key)
			return
		}

		acl := xmlACLToStorage(&aclPolicy)
		if err := h.storage.PutObjectACL(r.Context(), bucket, key, acl); err != nil {
			if errors.Is(err, storage.ErrBucketNotFound) {
				WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
				return
			}
			if errors.Is(err, storage.ErrObjectNotFound) {
				WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
				return
			}
			WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

// storageACLToXML converts a storage ACL to an XML AccessControlPolicy.
func storageACLToXML(acl *storage.ACL) *AccessControlPolicy {
	response := &AccessControlPolicy{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		Owner: Owner{
			ID:          acl.OwnerID,
			DisplayName: acl.OwnerDisplay,
		},
		AccessControlList: AccessControlList{
			Grants: make([]Grant, len(acl.Grants)),
		},
	}

	for i, g := range acl.Grants {
		grant := Grant{
			Permission: string(g.Permission),
		}

		if g.GranteeType == storage.ACLGranteeTypeCanonicalUser {
			grant.Grantee = Grantee{
				XsiType:     "CanonicalUser",
				ID:          g.GranteeID,
				DisplayName: g.GranteeID,
			}
		} else if g.GranteeType == storage.ACLGranteeTypeGroup {
			grant.Grantee = Grantee{
				XsiType: "Group",
				URI:     g.GranteeURI,
			}
		}

		response.AccessControlList.Grants[i] = grant
	}

	return response
}

// xmlACLToStorage converts an XML AccessControlPolicy to a storage ACL.
func xmlACLToStorage(policy *AccessControlPolicy) *storage.ACL {
	acl := &storage.ACL{
		OwnerID:      policy.Owner.ID,
		OwnerDisplay: policy.Owner.DisplayName,
		Grants:       make([]storage.ACLGrant, len(policy.AccessControlList.Grants)),
	}

	for i, g := range policy.AccessControlList.Grants {
		grant := storage.ACLGrant{
			Permission: storage.ACLPermission(g.Permission),
		}

		switch g.Grantee.XsiType {
		case "CanonicalUser":
			grant.GranteeType = storage.ACLGranteeTypeCanonicalUser
			grant.GranteeID = g.Grantee.ID
		case "Group":
			grant.GranteeType = storage.ACLGranteeTypeGroup
			grant.GranteeURI = g.Grantee.URI
		}

		acl.Grants[i] = grant
	}

	return acl
}
