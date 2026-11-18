---
name: verify-credentials
description: "Model-check how several holon processes share one credential file (pkg/installation/auth.go and credentials.go) with a TLA+ model. Use when a change touches withLock, token, loadCredentials, loginInteractive, logout, resourceToken, writeState or any other code that reads or writes the credential file or Client.state, and before deleting a check there that looks like it cannot fail."
---

# Verify the credential file protocol

Every `holon` process of one person shares one credential file per installation: `holon dev` in one terminal, `holon apps list` in another, two `holon login` prompts left open. The file holds one refresh token, from a sign-in that named no resource, and the access tokens renewed from it for each resource. Each process can renew that refresh token, which the identity provider then replaces, so the order in which processes read, renew and save decides whether the person stays signed in. A Go test runs one order. `Credentials.tla` in this directory is a TLA+ model of the protocol, and the TLC model checker runs every order of two processes within the bounds below.

## The rules

| Rule | What it says | What breaks for the person when it fails |
| --- | --- | --- |
| `RefreshTokenSpentOnce` | No process presents a refresh token that the identity provider has already replaced or revoked. | The provider refuses the token and the command stops with "sign-in expired" in the middle of work. |
| `StoredTokensAreLive` | Whenever no process holds the lock, every refresh token in the file is one the provider still accepts. | The next command anyone runs is signed out, although nothing went wrong. |
| `OneIdentityPerProcess` | A running process hands out tokens of one sign-in only: the one it first loaded, or the one it made itself. | A `holon dev` started as one account keeps working as another after someone signs in again in a second terminal. |
| `NewestSignInWins` | A sign-in never replaces credentials that were saved after it began. | A `holon login` left waiting in an old terminal overwrites the sign-in made since, and running commands lose their credentials. |

## What the model describes

Sync commit: `63c7b20` (holon-sh/cli#300). The model was last confirmed to describe the code at this commit.

Each action in `Credentials.tla` is one file read, one atomic rename, or one request to the identity provider.

| Actions | Code in `pkg/installation` |
| --- | --- |
| `TokenLock`, `Unlock` | `withLock` in `auth.go`: the `flock` on `<state file>.lock`, taken by `resourceToken` around `token` |
| `TokenLoad`, `Mismatch`, `Loadable` | `loadCredentials` and `readState` in `auth.go`, `readCredentialFile` in `credentials.go`; `Mismatch` is the `errCredentialChanged` check |
| `TokenCached`, `TokenRenew` | `token` and `renewToken` in `auth.go`; the model leaves out which resource a token is for, because every renewal presents the same refresh token |
| `TokenWrite`, `LoginWrite` | `writeState` and `writeAtomicFile` in `credentials.go` |
| `LoginBefore`, `LoginDevice`, `LoginLock`, `LoginCheck`, `LoginWrite` | `loginInteractive` in `auth.go` |
| `LogoutLock`, `LogoutRevoke`, `LogoutRemove` | `logout` and `revokeRefreshTokens` in `auth.go`, `removeCredentialFile` in `credentials.go` |
| `Restart` | `NewClient`: a new process starts with an empty `Client.state` |

The variables `identity`, `served`, `started`, `spent` and `stale` exist only so that the rules can be written. The code has no such fields.

## Does a change need this

A change needs the check when either command prints anything you cannot rule out:

```sh
git diff 63c7b20 -- pkg/installation/auth.go pkg/installation/credentials.go
```

```sh
git diff 63c7b20 -G'writeState|withLock|loadCredentials|readState|removeCredentialFile|statePath|m\.state' --stat -- . ':!pkg/installation/auth.go' ':!pkg/installation/credentials.go' ':!*_test.go'
```

The first shows edits to the covered code. The second shows other files that started to touch the file or `Client.state`. A change that only touches discovery, token validation, device-flow prompts, error wording or service-account tokens (`machineToken`) does not alter anything the model describes.

## Run it

Check `Credentials.tla` with the pstack `/statechart` skill, giving it `Credentials.cfg` as the config and `view.mjs` as the view. Read `/statechart` for how to run it and how to read its report. It installs its own pinned TLC and Java on first use.

On the sync commit the report lists all four rules and `NoDeadlock` under "Rules that hold", after 127,168 distinct states, in about ten seconds.

## Check that it can still fail

A model that cannot fail proves nothing. Each config in `faults/` sets `Fault` to one deliberate break of the code. Run each the same way and confirm that the rules named here break:

| Config | The code edit it stands for | Rules that must break |
| --- | --- | --- |
| `faults/renew_unlocked.cfg` | `resourceToken` calls `token` without `withLock` | `RefreshTokenSpentOnce`, `StoredTokensAreLive`, `NewestSignInWins` |
| `faults/no_version_check.cfg` | `loadCredentials` no longer returns `errCredentialChanged` | `OneIdentityPerProcess` |
| `faults/no_login_check.cfg` | `loginInteractive` no longer compares `saved.Version` with `before.Version` | `NewestSignInWins` |

`/statechart` reports one break per rule and then stops early, so a fault run lists the other rules as not checked. That is expected. Run the faults after any edit to the model, and whenever a pass surprises you.

## Read a result

- **Every rule holds.** The protocol keeps its promises within the bounds. Say so with the bounds, not as "the code is correct".
- **A rule breaks.** The report gives the shortest path as numbered steps. Replay it against the real code, one step for each action, as a scratch Go test beside `TestConcurrentAuthentication` in `pkg/installation/auth_test.go` or by reading the functions in the table above.
  - If a step cannot happen in the code, the model is wrong. Fix the model and say what was wrong.
  - If every step can happen, it is a bug in the code. Report the path in the code's words, the harm from the table above, and the commit that introduced it. Do not weaken the rule, the bounds or the model to make it pass, and do not commit the scratch test.

## Change the model with the code

When a change to the covered code is intended, update the model from the code before you run it, never from what makes it pass.

- Keep one step in the model for each file read, rename, or provider request. If a change splits one into two, or moves one outside `withLock`, split or move the action.
- A value read outside the lock, such as `before` in `loginInteractive`, is its own action. So is anything that happens between taking the lock and releasing it.
- If the change adds a check that guards a rule, add a `Fault` value that removes it and a config in `faults/`, and add its row to the table above.
- Change a rule only when the code's promise changed on purpose, and quote the evidence: the doc comment, the test, or the commit message.
- New code that writes the file or `Client.state` needs an action even when it lives in another file.

After the model passes and its faults fail as listed, update the tables here, and set the sync commit to the commit you checked. Move the sync commit only then. When covered code has changed and nobody rechecked it, use `/pstack:maintain-formal-verification-skill`.

## Bounds and assumptions

A pass covers this much and no more.

- Two processes, at most three completed sign-ins (a sign-in may begin until two have completed), and four refresh tokens issued.
- The `flock` excludes every other process, a rename replaces the file in one step, and a reader sees the old file or the new one.
- No process dies, and no write fails, between the provider's answer to a renewal and the rename that saves it. If that happens the file keeps a spent token and `StoredTokensAreLive` no longer holds. The code cannot make those two steps one.
- The provider answers every renewal it performs, always replaces the refresh token, and stops accepting a token only when it replaces or revokes it. A provider that expires tokens by age is not modeled.
- A revocation at sign-out revokes the refresh token in the file, or fails.

Not covered: goroutines of one process sharing a `Client` (the `mu` mutex), how long a process waits for the lock while another holds it across a network call, service-account tokens, file permissions and symlink checks, a file written for another installation or issuer, and the device flow itself.
