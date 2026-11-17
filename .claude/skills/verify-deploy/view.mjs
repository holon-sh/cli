const where = {
  start: "not started",
  building: "build call in flight",
  mount: "built, mounting the development image",
  publish: "publishing the release",
  done: "released",
  stopped: "stopped",
};

const image = (i) => `${i.deploy} on the ${i.base} image, built on the ${i.on}`;

const layer = (t) => (t[1] === "base" ? `the ${t[2]} image's layers` : t[1] === "source" ? `${t[3]}'s source built on the ${t[2]}` : `dependencies built on the ${t[2]}`);

export const state = (v) => {
  const regions = {};
  for (const d of Object.keys(v.pc)) {
    const pod = v.pod[d] === "none" ? "" : `, pod on the ${v.pod[d]} image`;
    const on = v.assembledOn[d] === "none" ? "" : `, assembling on the ${v.assembledOn[d]}`;
    regions[`Deploy ${d}`] = where[v.pc[d]] + pod + on;
  }
  regions.Platform = `new pods start on the ${v.ctrlImage} image; development holds ${v.base.join(" and ")}, tags the ${v.tagged}`;
  regions["App repository"] = v.blobs.length ? v.blobs.map(layer).join(", ") : "no layers";
  regions.Releases = v.released.length ? v.released.map(image).join("; ") : "none";
  return regions;
};

const steps = {
  Start: "the CLI sends the build call",
  Freeze: "the pod pushes its layers",
  Answer: "the pod answers",
  Mount: "the CLI mounts the development image",
  Publish: "the CLI pushes the image and tags the release",
  Stop: "the deploy stops",
  PodRestarts: "the build pod restarts",
  ControllerUpgrades: "the upgrade rolls the controller",
  HookCopies: "the upgrade hook copies the new image",
  Collect: "the registry collects untagged content",
  Mark: "the collection marks",
  Sweep: "the collection sweeps",
  Finished: null,
};

export const step = (action, from, to) => {
  const label = steps[action];
  if (!label || action === "Collect") return label ?? null;
  const who = Object.keys(from.pc).find((d) => ["pc", "call", "pod"].some((key) => from[key][d] !== to[key][d]));
  return who ? `${label} (${who})` : label;
};

export const title = "holon deploy: releases from deploys at once";
