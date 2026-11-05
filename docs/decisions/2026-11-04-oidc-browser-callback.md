# OIDC web browser callback state binding

**Status: принято 2026-11-05**

## Контекст

The web authorization-code flow must return to Kubron without requiring the user to copy the final redirect URL. The broker creates a challenge ID only after a provider's `Begin` call returns, while OAuth authorization parameters (including `state`) are built inside the provider. The callback must also work when the redirect reaches a different server replica.

## Решение

- Use the broker-generated 128-bit random challenge ID as the OAuth `state` value for `browser-callback`. When the broker persists the challenge, it replaces the provisional state in the authorization URL with that ID. The provider validates the callback state against `req.Challenge.ID`.
- Treat the challenge ID as an unpredictable correlation value, not as a bearer credential. Possession alone is insufficient: the callback handler requires the authenticated web-session cookie, and the broker verifies that the challenge record's `SessionID` matches that session.
- Resolve the callback challenge through the shared `ChallengeStore`, not a process-local state map. The callback then uses the normal Answer claim/CAS path; successful completion consumes the challenge once and replay returns the same generic failure as an unknown or foreign challenge.
- Use the complete callback URL supplied by the HTTP request, but accept it only when scheme, host and path match the server-controlled redirect URI derived from validated `KUBRON_PUBLIC_URL`. The provider also validates the OAuth response state and RFC 9207 `iss` parameter when advertised, then exchanges the code with the PKCE verifier and verifies the ID token nonce.
- The callback is a top-level GET navigation from the identity provider. It therefore uses the existing `SameSite=Lax` HttpOnly session cookie and does not require the RPC CSRF header. CSRF protection is supplied by the unguessable state, challenge/session binding, exact redirect validation and one-time challenge consumption.
- Polling a `browser-callback` challenge is read-only, so a periodic UI poll cannot claim the challenge while the browser redirect is trying to complete it. The callback uses the same per-user and per-IdP request limiters as RPC challenge answers and returns `429` with `Retry-After` when limited.
- Return a fixed success or failure page. Set `Cache-Control: no-store` and `Referrer-Policy: no-referrer`; do not reflect callback parameters or write authorization codes, state values, or full callback URLs to logs or metrics.
- Keep the callback endpoint available only in the web server. Desktop authorization-code sign-in continues to use its loopback listener; web sign-in without a configured public client/redirect continues to use the existing paste-redirect flow.

## Consequences and residual risks

- The challenge ID is visible in the authorization URL and returned challenge object. This is acceptable only because it is generated with 128 bits of cryptographic randomness and is always checked against the authenticated session; it must not be logged or treated as an independent credential.
- The browser must return to the same Kubron origin that issued the challenge so its session cookie is sent. Deployments behind a TLS-terminating proxy must configure trusted proxy handling correctly.
- A challenge stored only in the in-memory store remains local to one process. Multi-replica deployments must use the shared PostgreSQL challenge store, as they already must for other cross-replica sign-in challenges.
- A callback with an invalid redirect or state is rejected without reflecting the supplied values. A valid but declined authorization consumes the challenge, and the user must start a new sign-in.

## Verification

- Provider tests cover the browser callback flow, PKCE exchange, redirect host/path validation, state mismatch and RFC 9207 issuer mismatch.
- Broker tests cover challenge ID/state equality, foreign-session rejection, one-time completion and absence of state/code in logs.
- HTTP tests cover authenticated-session routing, no session, missing/duplicate state, fixed response pages, and no-store/referrer headers.
- CI is required before merge; the decision remains subject to explicit human security review.

## Отмена

Remove the web callback route and provider flow, restore paste-redirect as the web authorization-code fallback, and remove the callback broker methods and tests. Existing desktop loopback and device-code flows remain unchanged.
