const loop = {
  select: "waiting for a change",
  pack: "packing the source",
  send: "sending the source",
  record: "served, about to record",
};

const shown = {
  working: "working",
  ready: "ready",
  failed: "failed",
  unrecorded: "serves this save, but a restart loses it",
};

const flying = (v) => Object.values(v.rec).filter((r) => r.stage === "uploading" || r.stage === "tag_sent");

const uploads = (v) => {
  const live = flying(v).map((r) => (r.cancel === "none" ? `save ${r.content}` : `save ${r.content} (cancelled)`));
  const late = v.wire.length > live.length ? ", a cancelled tag request still on its way" : "";
  return live.length || late ? `uploading ${live.join(" and ") || "nothing"}${late}` : "none in flight";
};

export const state = (v) => ({
  Disk: `source ${v.disk}`,
  Watcher: v.offer ? "offering a change" : v.pending ? "holding a change" : "nothing pending",
  Loop: loop[v.pc],
  "Status line": shown[v.shown],
  Runtime: v.served === "none" ? "serves nothing" : `serves ${v.served}`,
  "Registry tag": `names ${v.tag}`,
  Uploads: uploads(v),
});

const steps = {
  Save: "you save the file",
  DebounceFires: "the debounce time passes",
  LoopTakes: "the loop takes the change",
  Pack: "the loop packs the source",
  SendServed: "the runtime serves the new source",
  SendRefused: "the runtime refuses the new source",
  Record: "the loop starts the upload and shows the app ready",
  RecSendsTag: "an upload sends its tag request",
  RegistryTags: "the registry moves the tag",
  RecSucceeds: "an upload ends in success",
  RecTimesOut: "an upload passes its one minute",
  RecEndsWithError: "an upload ends with an error",
  Restart: "the app's pod restarts",
  Settled: null,
};

export const step = (action) => steps[action] ?? null;

export const title = "holon dev: one app's saves";
