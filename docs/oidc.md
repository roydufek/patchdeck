# Single sign-on (OIDC)

Patchdeck can log you in through any standard OpenID Connect provider using the
Authorization Code flow with PKCE. Local username/password + TOTP keep working as a
**break-glass** login, so a provider outage never locks you out.

Because Patchdeck is single-admin, an SSO login is authenticated by your provider and then
granted the existing admin session — there is no per-user account to create or match. Decide
*who* may sign in by restricting the client in your provider (recommended), and/or with the
optional allowlist in Patchdeck.

## Setup with Pocket ID

1. In **Pocket ID**, create a new **OIDC client**:
   - **Callback URL**: copy it from Patchdeck → Settings → *Single sign-on (OIDC)* → the
     read-only **Redirect / callback URL** field (it is `https://<your-patchdeck>/auth/oidc/callback`).
   - Restrict access to the users or groups who should reach Patchdeck (this is your primary
     access control).
   - Note the generated **Client ID** and **Client secret**.
2. In **Patchdeck** → Settings → *Single sign-on (OIDC)*:
   - **Issuer / provider URL**: your Pocket ID base URL (e.g. `https://id.example.com`).
     Patchdeck discovers the endpoints from `<issuer>/.well-known/openid-configuration`.
   - **Client ID** / **Client secret**: from step 1. The secret is encrypted at rest and is
     write-only (blank leaves the stored one unchanged).
   - **Public base URL** *(optional)*: set this if the auto-detected callback origin is ever
     wrong behind your reverse proxy; otherwise leave blank.
   - **Allowed emails or groups** *(optional)*: a second gate on top of the provider. Leave
     blank to let Pocket ID be the sole gate. Email entries are only honored when the
     provider asserts `email_verified: true` (so an unverified email can never satisfy the
     allowlist) — if your provider doesn't emit `email_verified`, allowlist by **group**
     instead, or just restrict the client in the provider.
   - Tick **Enabled** and **Save**.
3. Sign out and confirm the **Sign in with SSO** button appears on the login page.

## Notes

- **Any standard OIDC provider works** (Authentik, Authelia, Keycloak, Zitadel, …) — the
  steps are the same; only the admin UI differs.
- **MFA** is handled by your provider for SSO logins; Patchdeck's own TOTP still guards the
  local break-glass login.
- **Behind a reverse proxy**, Patchdeck derives the callback origin from `X-Forwarded-Proto`
  / `X-Forwarded-Host`. Set **Public base URL** to override if needed.
- **Automation** (the `pd_` API tokens) is unaffected by SSO.
