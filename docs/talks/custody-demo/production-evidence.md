# Custody production evidence (sanitized)

**Captured:** 2026-07-26 on the operator's corporate network.  
**Purpose:** production evidence after the fake-credential mechanism demo.

The caller made a loopback request with an `X-Custody-Grant` and no Jira PAT:

```text
GET http://127.0.0.1:8127/jira/rest/api/2/myself
  custody: verdict=pass, upstream_status=200
  Jira: real user profile returned
```

Custody read the PAT from the OS credential store, injected
`Authorization: Bearer <PAT>`, forwarded the request to a corporate Jira Data
Center deployment, and streamed the response. The caller never received or sent
the PAT.

The same manifest and read grant held the scope boundary:

```text
POST /rest/api/2/issue
  custody: HTTP 403 denied_no_action_match
  upstream: not contacted

GET /rest/api/3/issue/<redacted>
  custody: HTTP 403 denied_no_action_match
  upstream: not contacted
```

A specific issue read also returned `200`. Its first attempt included an
unlisted `fields` query parameter and failed closed at custody with `403`; the
operator deliberately enabled extra query parameters on that read rule, then
the same request passed. No ticket key, title, body, host, user identity, or
credential is retained here.

Confluence is not production-proven: custody forwarded the request, but the
upstream returned `403`. Do not collapse that into the Jira claim.

## Safe stage claim

> A general local credential broker read a real corporate Jira while the caller
> held a scoped, expiring capability instead of the operator's PAT.

## Limits

- Single operator and single machine; not a team gateway.
- The grant is a replayable loopback bearer until expiry.
- Custody protects the credential, not the response body.
- This replaces a per-agent credential proxy, not the existing corporate gateway.
