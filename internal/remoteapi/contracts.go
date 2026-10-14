package remoteapi

import (
	"fmt"
	"strings"
	"time"
)

type RemoteUser struct {
	ID          string `json:"id"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
}

type DeviceStartRequest struct {
	DeviceName      string `json:"deviceName"`
	Platform        string `json:"platform"`
	HarnessLocalURL string `json:"harnessLocalUrl,omitempty"`
}

type Device struct {
	DeviceID        string         `json:"deviceId"`
	UserID          FlexibleString `json:"userId"`
	DeviceName      string         `json:"deviceName,omitempty"`
	Platform        string         `json:"platform,omitempty"`
	HarnessLocalURL string         `json:"harnessLocalUrl,omitempty"`
	Status          string         `json:"status,omitempty"`
}

type TunnelGrantRequest struct {
	DeviceID        string `json:"deviceId"`
	HarnessLocalURL string `json:"harnessLocalUrl,omitempty"`
}

type ClientLoginExchangeRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Code            string `json:"code"`
	State           string `json:"state,omitempty"`
	HarnessLocalURL string `json:"harnessLocalUrl,omitempty"`
	// ClientInstanceID identifies the installation that started the login. It is
	// sent here as well as in the login URL so a login that was interrupted, or
	// one started from a link that has been sitting in a browser tab, is still
	// attributed to the installation that asked for it.
	ClientInstanceID string `json:"clientInstanceId,omitempty"`
}

// EntitlementStatus is the Remote subscription state Penguin Go reports for an
// account. It is the only vocabulary the client treats as valid.
type EntitlementStatus string

const (
	// EntitlementStatusActive means the subscription is paid and not expired.
	// An active status always comes with a tunnel grant and an expiry time.
	EntitlementStatusActive EntitlementStatus = "active"
	// EntitlementStatusExpired means the subscription lapsed. The tunnel is null.
	EntitlementStatusExpired EntitlementStatus = "expired"
	// EntitlementStatusNone means the account never had Remote provisioned.
	// The expiry is null and the tunnel is null.
	EntitlementStatusNone EntitlementStatus = "none"
)

// Known reports whether the status is one Penguin Go documents. Anything else,
// including an absent status, is treated as unknown by the client instead of
// being guessed at.
func (s EntitlementStatus) Known() bool {
	switch s {
	case EntitlementStatusActive, EntitlementStatusExpired, EntitlementStatusNone:
		return true
	default:
		return false
	}
}

// RemoteEntitlement is the subscription state Penguin Go returns alongside a
// client login exchange.
type RemoteEntitlement struct {
	Status EntitlementStatus `json:"status"`
	// ExpiresAt is the UTC RFC3339/ISO 8601 expiry. It is null when Status is
	// EntitlementStatusNone. It stays a raw string so an unparsable value fails
	// closed at the point of use instead of aborting the whole exchange.
	ExpiresAt *string `json:"expiresAt"`
	// RenewalURL is where a lapsed subscriber can renew. Penguin Go does not
	// serve a renewal page yet, so this is normally empty and the client must
	// never link to a guessed Penguin Go URL instead.
	RenewalURL string `json:"renewalUrl,omitempty"`
	// MaxConcurrentTunnels is how many tunnels this account may run at the same
	// time. It is a pointer so an older Penguin Go that does not send it is
	// distinguishable from one that sent a value.
	MaxConcurrentTunnels *int `json:"maxConcurrentTunnels,omitempty"`
}

// ExpiresAtTime parses the raw expiry. The boolean reports whether a timestamp
// was present at all, so callers can tell "no expiry" from "expired at epoch".
func (e RemoteEntitlement) ExpiresAtTime() (time.Time, bool, error) {
	return ParseOptionalTime(e.ExpiresAt, "entitlement expiresAt")
}

// RemoteLease is the time-bounded grant that lets one installation hold one
// concurrent tunnel slot.
//
// LeaseID is a credential: it may be stored in the permission-restricted local
// config, but it must never be written to a log, shown to the user or embedded
// in an error message.
type RemoteLease struct {
	LeaseID   string `json:"leaseId"`
	SlotIndex *int   `json:"slotIndex,omitempty"`
	// Generation is a fencing token that changes whenever the slot is handed
	// out again. Penguin Go may send it as a number or as a string, so it is
	// decoded flexibly and compared as text.
	Generation FlexibleString `json:"generation,omitempty"`
	DeviceID   string         `json:"deviceId,omitempty"`
	// ExpiresAt is when this lease stops authorising the slot. It is null when
	// Penguin Go did not send one, which the client treats as "no usable lease".
	ExpiresAt *string `json:"expiresAt"`
	RenewedAt *string `json:"renewedAt,omitempty"`
}

func (l RemoteLease) ExpiresAtTime() (time.Time, bool, error) {
	return ParseOptionalTime(l.ExpiresAt, "lease expiresAt")
}

func (l RemoteLease) RenewedAtTime() (time.Time, bool, error) {
	return ParseOptionalTime(l.RenewedAt, "lease renewedAt")
}

// LeasePath is where a running tunnel renews its slot lease.
const LeasePath = "/api/remote/client/lease"

// LeaseCodeSuperseded is the code Penguin Go returns when the slot described by
// the request has been handed to another device of the same account. The client
// must stop its tunnel and must not ask for the slot again on its own.
const LeaseCodeSuperseded = "remote_lease_superseded"

// LeaseRenewRequest is the body of POST LeasePath.
type LeaseRenewRequest struct {
	DeviceID string `json:"deviceId"`
	// LeaseID is a credential; it is never logged and never echoed into an error.
	LeaseID string `json:"leaseId"`
}

// LeaseRenewResponse is what Penguin Go answers a lease renewal with.
type LeaseRenewResponse struct {
	// Current reports whether this installation still holds the slot.
	Current bool `json:"current"`
	// Code carries the machine-readable reason when the renewal was refused,
	// for example LeaseCodeSuperseded.
	Code string `json:"code,omitempty"`
	// Lease is the renewed grant. It may be absent when nothing about the lease
	// changed.
	Lease *RemoteLease `json:"lease,omitempty"`
	// ExpiresAt is a lease expiry sent at the top level rather than inside the
	// lease object.
	ExpiresAt *string `json:"expiresAt,omitempty"`
	// TTLSeconds is how long Penguin Go wants the client to wait before it asks
	// again. Zero means the server will not renew the lease any further.
	TTLSeconds *int `json:"ttlSeconds,omitempty"`
	// Entitlement is the subscription state as of this renewal.
	Entitlement *RemoteEntitlement `json:"entitlement,omitempty"`
}

// ParseOptionalTime parses an optional RFC3339 timestamp. The boolean reports
// whether a timestamp was present at all, so callers can tell "absent" from
// "invalid", and only an invalid value is an error.
func ParseOptionalTime(raw *string, field string) (time.Time, bool, error) {
	if raw == nil {
		return time.Time{}, false, nil
	}
	text := strings.TrimSpace(*raw)
	if text == "" {
		return time.Time{}, false, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse %s %q: %w", field, text, err)
	}
	return parsed.UTC(), true, nil
}

type ClientLoginExchangeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	DeviceAccessToken string            `json:"deviceAccessToken"`
	Device            Device            `json:"device"`
	User              *RemoteUser       `json:"user,omitempty"`
	Entitlement       RemoteEntitlement `json:"entitlement"`
	// Tunnel is null whenever the subscription is not active, so it must stay a
	// pointer: `"tunnel": null` has to decode as "no tunnel" rather than as a
	// zero-valued TunnelGrant that looks like a real grant.
	Tunnel *TunnelGrant `json:"tunnel"`
	// Lease is the slot lease that comes with an active subscription. It is nil
	// on a Penguin Go that predates the lease protocol, which the client records
	// as an explicitly unsupported state instead of running the tunnel as if the
	// slot had been granted permanently.
	Lease *RemoteLease `json:"lease"`
}

type TunnelGrant struct {
	TunnelID     string         `json:"tunnelId,omitempty"`
	DeviceID     string         `json:"deviceId"`
	UserID       FlexibleString `json:"userId,omitempty"`
	ProxyName    string         `json:"proxyName,omitempty"`
	PublicHost   string         `json:"publicHost,omitempty"`
	ServerAddr   string         `json:"serverAddr"`
	ServerPort   int            `json:"serverPort"`
	Token        string         `json:"token"`
	PublicURL    string         `json:"publicUrl"`
	CustomDomain string         `json:"customDomain,omitempty"`
	Subdomain    string         `json:"subdomain,omitempty"`
	Status       string         `json:"status,omitempty"`
	// SlotIndex is the concurrency slot this tunnel occupies. It is a pointer so
	// an older Penguin Go that does not send it is distinguishable from one that
	// sent slot 0.
	SlotIndex *int `json:"slotIndex,omitempty"`
}
