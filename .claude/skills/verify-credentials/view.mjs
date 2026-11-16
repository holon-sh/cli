const doing = {
  idle: "idle",
  token_load: "holds the lock, about to read the file",
  token_use: "holds the lock, has read the file",
  token_write: "holds the lock, renewed, not yet saved",
  unlock: "about to release the lock",
  unlock_signin: "about to release the lock and ask for a session sign-in",
  signin_device: "waiting for a session sign-in",
  signin_lock: "has a session token, waiting for the lock",
  signin_load: "holds the lock with a session token, about to read the file",
  signin_write: "holds the lock, about to save the session token",
  login_device: "holon login waiting for approval",
  login_lock: "holon login approved, waiting for the lock",
  login_check: "holon login holds the lock, about to compare versions",
  login_write: "holon login holds the lock, about to save",
  logout_revoke: "holon logout holds the lock, about to revoke",
  logout_remove: "holon logout holds the lock, about to remove the file",
};

const disk = (v) =>
  v.file.version === 0 ? "no file" : `file of sign-in ${v.file.version}`;

const who = (v, p) => {
  const signedIn = v.identity[p] === 0 ? "not signed in" : `signed in as ${v.identity[p]}`;
  const wrong =
    v.served[p] !== 0 && v.served[p] !== v.identity[p]
      ? `, handed out a token of sign-in ${v.served[p]}`
      : "";
  return `${doing[v.pc[p]]} (${signedIn}${wrong})`;
};

const provider = (v) => {
  const dead = Object.values(v.file.tokens).filter((t) => t !== 0 && !v.live.includes(t));
  const notes = [];
  if (dead.length) notes.push("the file holds a spent refresh token");
  if (v.spent) notes.push("a spent refresh token was presented");
  if (v.stale) notes.push("a sign-in replaced a newer one");
  return notes.length ? notes.join(", ") : "ok";
};

export const state = (v) => {
  const regions = { Disk: disk(v), Tokens: provider(v) };
  for (const p of Object.keys(v.pc)) regions[`holon ${p}`] = who(v, p);
  return regions;
};

const steps = {
  TokenLock: "takes the lock for a token",
  TokenLoad: "reads the file",
  TokenCached: "returns its cached access token",
  TokenRenew: "presents its refresh token",
  TokenWrite: "saves the renewed token",
  SigninDevice: "gets a session token from the device flow",
  SigninLock: "takes the lock to save the session token",
  SigninLoad: "reads the file again",
  SigninWrite: "saves the session token",
  LoginBefore: "starts holon login and notes the version on disk",
  LoginDevice: "gets an admin token from the device flow",
  LoginLock: "takes the lock to save the sign-in",
  LoginCheck: "compares the version on disk with the one it noted",
  LoginWrite: "saves the new sign-in",
  LogoutLock: "takes the lock for holon logout",
  LogoutRevoke: "asks the identity provider to revoke the tokens in the file",
  LogoutRemove: "removes the file",
  Unlock: "releases the lock",
  Restart: "exits, and a new holon starts",
  Exhausted: null,
};

export const step = (action, from, to) => {
  const label = steps[action];
  if (!label) return null;
  const who = Object.keys(from.pc).find(
    (p) =>
      from.pc[p] !== to.pc[p] ||
      JSON.stringify(from.mem[p]) !== JSON.stringify(to.mem[p]) ||
      from.identity[p] !== to.identity[p] ||
      from.served[p] !== to.served[p],
  );
  return who ? `holon ${who} ${label}` : label;
};

export const title = "holon processes sharing one credential file";
