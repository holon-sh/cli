------------------------------ MODULE Credentials ------------------------------
\* Several holon processes sharing one credential file, as pkg/installation/auth.go
\* and credentials.go keep it. Every action names the code it stands for. A step
\* here is one file read, one atomic rename, or one identity provider request.
EXTENDS Naturals

CONSTANTS
    Procs,      \* holon processes that share one credential file
    Audiences,  \* resources a refresh token is issued for
    Admin,      \* the audience that holon login signs in for
    MaxLogins,  \* a sign-in may begin until this many have completed
    MaxTokens,  \* refresh tokens the identity provider may issue
    Fault       \* "none", or one deliberate break of the code (see SKILL.md)

VARIABLES
    file,         \* the credential file: authState, version None when absent
    live,         \* refresh tokens the identity provider still accepts
    lock,         \* holder of the flock on the file, or "free"
    mem,          \* each process's Client.state
    pc,           \* where each process is
    aud,          \* the audience a process wants a token for
    before,       \* loginInteractive's before.Version
    minted,       \* a device-flow token a process holds and has not saved
    nextVersion, nextToken,
    \* What the rules below are written against. The code has no such fields.
    identity,     \* the sign-in a process first loaded, or made itself
    served,       \* the sign-in whose token a process last handed to a caller
    started,      \* the sign-in on disk when a process began holon login
    spent,        \* a process presented a refresh token that was already spent
    stale         \* a sign-in replaced credentials saved after it began

vars == <<file, live, lock, mem, pc, aud, before, minted, nextVersion, nextToken,
          identity, served, started, spent, stale>>

None == 0
Empty == [version |-> None, tokens |-> [a \in Audiences |-> None]]

Init ==
    /\ file = Empty
    /\ live = {}
    /\ lock = "free"
    /\ mem = [p \in Procs |-> Empty]
    /\ pc = [p \in Procs |-> "idle"]
    /\ aud = [p \in Procs |-> Admin]
    /\ before = [p \in Procs |-> None]
    /\ minted = [p \in Procs |-> None]
    /\ nextVersion = 1
    /\ nextToken = 1
    /\ identity = [p \in Procs |-> None]
    /\ served = [p \in Procs |-> None]
    /\ started = [p \in Procs |-> None]
    /\ spent = FALSE
    /\ stale = FALSE

Go(p, to) == pc' = [pc EXCEPT ![p] = to]

\* loadCredentials refuses a file another sign-in wrote (errCredentialChanged).
Mismatch(p) ==
    /\ Fault # "no_version_check"
    /\ mem[p].version # None
    /\ mem[p].version # file.version

\* loadCredentials succeeds: the file exists and is this process's sign-in.
Loadable(p) == file.version # None /\ ~Mismatch(p)

Load(p) ==
    /\ mem' = [mem EXCEPT ![p] = file]
    /\ identity' = [identity EXCEPT ![p] = IF mem[p].version = None THEN file.version ELSE @]

----------------------------------------------------------------------------
\* resourceToken: withLock around token (auth.go).

\* withLock takes the flock for token.
TokenLock(p, a) ==
    /\ pc[p] = "idle"
    /\ IF Fault = "renew_unlocked" THEN UNCHANGED lock ELSE lock = "free" /\ lock' = p
    /\ aud' = [aud EXCEPT ![p] = a]
    /\ Go(p, "token_load")
    /\ UNCHANGED <<file, live, mem, before, minted, nextVersion, nextToken,
                   identity, served, started, spent, stale>>

\* token calls loadCredentials, then looks for the audience's token.
TokenLoad(p) ==
    /\ pc[p] = "token_load"
    /\ IF ~Loadable(p)
         THEN Go(p, "unlock") /\ UNCHANGED <<mem, identity>>
         ELSE /\ Load(p)
              /\ IF file.tokens[aud[p]] = None
                   \* errNoAudienceToken: an error, or a device sign-in at a terminal.
                   THEN \E to \in {"unlock", "unlock_signin"} : Go(p, to)
                   ELSE Go(p, "token_use")
    /\ UNCHANGED <<file, live, lock, aud, before, minted, nextVersion, nextToken,
                   served, started, spent, stale>>

\* token returns the cached access token, which has not expired.
TokenCached(p) ==
    /\ pc[p] = "token_use"
    /\ served' = [served EXCEPT ![p] = mem[p].version]
    /\ Go(p, "unlock")
    /\ UNCHANGED <<file, live, lock, mem, aud, before, minted, nextVersion, nextToken,
                   identity, started, spent, stale>>

\* renewToken presents the refresh token. The identity provider rotates it, or
\* refuses one it has already rotated or revoked (errReauthenticationRequired).
TokenRenew(p) ==
    /\ pc[p] = "token_use"
    /\ LET old == mem[p].tokens[aud[p]] IN
         IF old \in live
           THEN /\ nextToken <= MaxTokens
                /\ live' = (live \ {old}) \cup {nextToken}
                /\ nextToken' = nextToken + 1
                /\ mem' = [mem EXCEPT ![p].tokens[aud[p]] = nextToken]
                /\ Go(p, "token_write")
                /\ UNCHANGED spent
           ELSE /\ spent' = TRUE
                /\ Go(p, "unlock")
                /\ UNCHANGED <<live, nextToken, mem>>
    /\ UNCHANGED <<file, lock, aud, before, minted, nextVersion, identity, served,
                   started, stale>>

\* token saves the whole state with writeState, an atomic rename.
TokenWrite(p) ==
    /\ pc[p] = "token_write"
    /\ file' = mem[p]
    /\ served' = [served EXCEPT ![p] = mem[p].version]
    /\ Go(p, "unlock")
    /\ UNCHANGED <<live, lock, mem, aud, before, minted, nextVersion, nextToken,
                   identity, started, spent, stale>>

----------------------------------------------------------------------------
\* resourceToken's errNoAudienceToken branch: deviceToken outside the lock, then
\* withLock around loadCredentials and writeState.

\* deviceToken: the person approves, and the identity provider issues a token.
SigninDevice(p) ==
    /\ pc[p] = "signin_device"
    /\ nextToken <= MaxTokens
    /\ live' = live \cup {nextToken}
    /\ minted' = [minted EXCEPT ![p] = nextToken]
    /\ nextToken' = nextToken + 1
    /\ Go(p, "signin_lock")
    /\ UNCHANGED <<file, lock, mem, aud, before, nextVersion, identity, served,
                   started, spent, stale>>

SigninLock(p) ==
    /\ pc[p] = "signin_lock"
    /\ lock = "free"
    /\ lock' = p
    /\ Go(p, "signin_load")
    /\ UNCHANGED <<file, live, mem, aud, before, minted, nextVersion, nextToken,
                   identity, served, started, spent, stale>>

\* loadCredentials again, under the lock, before the new token is added.
SigninLoad(p) ==
    /\ pc[p] = "signin_load"
    /\ IF Fault = "write_without_load"
         THEN Go(p, "signin_write") /\ UNCHANGED <<mem, identity>>
         ELSE IF ~Loadable(p)
                THEN Go(p, "unlock") /\ UNCHANGED <<mem, identity>>
                ELSE Load(p) /\ Go(p, "signin_write")
    /\ UNCHANGED <<file, live, lock, aud, before, minted, nextVersion, nextToken,
                   served, started, spent, stale>>

\* m.state.Tokens[audience] = token, then writeState.
SigninWrite(p) ==
    /\ pc[p] = "signin_write"
    /\ LET next == [mem[p] EXCEPT !.tokens[aud[p]] = minted[p]] IN
         /\ mem' = [mem EXCEPT ![p] = next]
         /\ file' = next
         /\ served' = [served EXCEPT ![p] = next.version]
    /\ Go(p, "unlock")
    /\ UNCHANGED <<live, lock, aud, before, minted, nextVersion, nextToken,
                   identity, started, spent, stale>>

----------------------------------------------------------------------------
\* loginInteractive (holon login, or the sign-in a signed-out command starts).

\* before, _ := m.readState(), without the lock.
LoginBefore(p) ==
    /\ pc[p] = "idle"
    /\ nextVersion <= MaxLogins
    /\ before' = [before EXCEPT ![p] = file.version]
    /\ started' = [started EXCEPT ![p] = file.version]
    /\ Go(p, "login_device")
    /\ UNCHANGED <<file, live, lock, mem, aud, minted, nextVersion, nextToken,
                   identity, served, spent, stale>>

\* deviceToken for the admin audience.
LoginDevice(p) ==
    /\ pc[p] = "login_device"
    /\ nextToken <= MaxTokens
    /\ live' = live \cup {nextToken}
    /\ minted' = [minted EXCEPT ![p] = nextToken]
    /\ nextToken' = nextToken + 1
    /\ Go(p, "login_lock")
    /\ UNCHANGED <<file, lock, mem, aud, before, nextVersion, identity, served,
                   started, spent, stale>>

LoginLock(p) ==
    /\ pc[p] = "login_lock"
    /\ lock = "free"
    /\ lock' = p
    /\ Go(p, "login_check")
    /\ UNCHANGED <<file, live, mem, aud, before, minted, nextVersion, nextToken,
                   identity, served, started, spent, stale>>

\* saved.Version != before.Version is errCredentialChanged.
LoginCheck(p) ==
    /\ pc[p] = "login_check"
    /\ IF Fault # "no_login_check" /\ file.version # before[p]
         THEN Go(p, "unlock")
         ELSE Go(p, "login_write")
    /\ UNCHANGED <<file, live, lock, mem, aud, before, minted, nextVersion, nextToken,
                   identity, served, started, spent, stale>>

\* writeState(state) with a new Version and only the admin token; m.state = state.
LoginWrite(p) ==
    /\ pc[p] = "login_write"
    /\ LET new == [version |-> nextVersion,
                   tokens |-> [a \in Audiences |-> IF a = Admin THEN minted[p] ELSE None]] IN
         /\ file' = new
         /\ mem' = [mem EXCEPT ![p] = new]
    /\ stale' = (stale \/ (file.version # None /\ file.version # started[p]))
    /\ identity' = [identity EXCEPT ![p] = nextVersion]
    /\ served' = [served EXCEPT ![p] = None]
    /\ nextVersion' = nextVersion + 1
    /\ Go(p, "unlock")
    /\ UNCHANGED <<live, lock, aud, before, minted, nextToken, started, spent>>

----------------------------------------------------------------------------
\* logout: withLock around revokeRefreshTokens and removeCredentialFile.

LogoutLock(p) ==
    /\ pc[p] = "idle"
    /\ lock = "free"
    /\ lock' = p
    /\ Go(p, "logout_revoke")
    /\ UNCHANGED <<file, live, mem, aud, before, minted, nextVersion, nextToken,
                   identity, served, started, spent, stale>>

\* revokeRefreshTokens revokes every token in the file, or fails and revokes none.
LogoutRevoke(p) ==
    /\ pc[p] = "logout_revoke"
    /\ live' \in {live, live \ {file.tokens[a] : a \in Audiences}}
    /\ Go(p, "logout_remove")
    /\ UNCHANGED <<file, lock, mem, aud, before, minted, nextVersion, nextToken,
                   identity, served, started, spent, stale>>

\* removeCredentialFile.
LogoutRemove(p) ==
    /\ pc[p] = "logout_remove"
    /\ file' = Empty
    /\ Go(p, "unlock")
    /\ UNCHANGED <<live, lock, mem, aud, before, minted, nextVersion, nextToken,
                   identity, served, started, spent, stale>>

----------------------------------------------------------------------------

\* withLock's deferred Unlock, and the return to the caller.
Unlock(p) ==
    /\ pc[p] \in {"unlock", "unlock_signin"}
    /\ lock' = IF lock = p THEN "free" ELSE lock
    /\ IF pc[p] = "unlock_signin"
         THEN Go(p, "signin_device") /\ UNCHANGED <<aud, before, minted, started>>
         ELSE /\ Go(p, "idle")
              /\ aud' = [aud EXCEPT ![p] = Admin]
              /\ before' = [before EXCEPT ![p] = None]
              /\ minted' = [minted EXCEPT ![p] = None]
              /\ started' = [started EXCEPT ![p] = None]
    /\ UNCHANGED <<file, live, mem, nextVersion, nextToken, identity, served, spent, stale>>

\* The process exits and a new holon starts: NewClient with an empty state.
Restart(p) ==
    /\ pc[p] = "idle"
    /\ mem[p] # Empty \/ identity[p] # None \/ served[p] # None
    /\ mem' = [mem EXCEPT ![p] = Empty]
    /\ identity' = [identity EXCEPT ![p] = None]
    /\ served' = [served EXCEPT ![p] = None]
    /\ UNCHANGED <<file, live, lock, pc, aud, before, minted, nextVersion, nextToken,
                   started, spent, stale>>

\* The identity provider's budget of tokens is spent: the end of a bounded run.
Exhausted == nextToken > MaxTokens /\ UNCHANGED vars

Next ==
    \/ Exhausted
    \/ \E p \in Procs :
            \/ \E a \in Audiences : TokenLock(p, a)
            \/ TokenLoad(p)
            \/ TokenCached(p)
            \/ TokenRenew(p)
            \/ TokenWrite(p)
            \/ SigninDevice(p)
            \/ SigninLock(p)
            \/ SigninLoad(p)
            \/ SigninWrite(p)
            \/ LoginBefore(p)
            \/ LoginDevice(p)
            \/ LoginLock(p)
            \/ LoginCheck(p)
            \/ LoginWrite(p)
            \/ LogoutLock(p)
            \/ LogoutRevoke(p)
            \/ LogoutRemove(p)
            \/ Unlock(p)
            \/ Restart(p)

Spec == Init /\ [][Next]_vars

----------------------------------------------------------------------------
\* The rules.

\* No process presents a refresh token the identity provider no longer accepts.
RefreshTokenSpentOnce == ~spent

\* Whenever no process holds the lock, every refresh token in the file is one
\* the identity provider still accepts.
StoredTokensAreLive ==
    lock = "free" =>
        \A a \in Audiences : file.tokens[a] # None => file.tokens[a] \in live

\* A process hands its callers tokens of one sign-in only: the one it first
\* loaded, or the one it made itself.
OneIdentityPerProcess ==
    \A p \in Procs : served[p] # None => served[p] = identity[p]

\* A sign-in never replaces credentials that were saved after it began.
NewestSignInWins == ~stale

=============================================================================
