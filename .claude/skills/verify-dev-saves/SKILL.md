---
name: verify-dev-saves
description: "Model-check what holon dev does with a save (pkg/installation/dev.go and dev_reload.go) with a TLA+ model: the watcher's debounce, the loop's pack and send, the background recording to the registry, and the app's status line. Use when a change touches devWatcher.run, devLoop.run, rebuild, reload, reloadApp, send, record or publish, the d.sent or d.recording maps, the begin, fail, finish or problem calls that set an app's status, or how the runtime applies an overlay or boots from the session's tag."
---

# Verify what holon dev does with a save

A save in `holon dev` passes through four things that run at the same time: the watcher goroutine, the loop, one upload goroutine per save, and the runtime and registry on the other side of the network. Saves can arrive while any of them is busy. A Go test runs one order of events. `DevSaves.tla` in this directory is a TLA+ model of one source app in a ready session, and the TLC model checker runs every order within the bounds below.

## The rules

"Quiet" means the loop is waiting in its `select` and the watcher holds nothing for it.

| Rule | What it says | What breaks for the developer when it fails |
| --- | --- | --- |
| `ReadyServesLastSave` | When it is quiet and the app is shown ready, the runtime serves the source that is on disk. | They test old code and believe it is the save they just made. |
| `RestartKeepsLastSave` | When it is quiet, the app is shown ready and every upload has ended, the registry tag names the source that is on disk. | A restarted pod brings back an older save, and nothing said so. |
| `FailureIsCurrent` | When it is quiet and the app is shown failed, its current source was sent and refused. | The app stays failed after they undo the edit that broke it. |
| `RefusalStaysShown` | When it is quiet and the current source was refused, the app is shown failed. | The failure leaves the status line, and they believe the app runs the save. |

The first three hold on the sync commit under the assumptions below. `RefusalStaysShown` does not: see Open findings.

## What the model describes

Sync commit: `54f44c8`. The model was last confirmed to describe the CLI at this commit. The runtime and registry behavior was read from the platform at `818db651` and the runtime at `7da1e52`.

| Actions | Code |
| --- | --- |
| `Save`, `DebounceFires` | `devWatcher.run` in `pkg/installation/dev.go`: `pending`, the `debounce` timer, and `output = w.changes` |
| `LoopTakes` | `case change := <-changes` in `devLoop.run`, then `rebuild` |
| `Pack` | `reload` and `reloadApp` in `dev_reload.go`: `pack`, the comparison with `d.sent`, and `begin` |
| `SendServed`, `SendRefused` | `send` in `dev_reload.go`; on an error `reloadApp` deletes `d.sent` and `reload` calls `fail` |
| `Record` | `record` in `dev_reload.go` (the `errSourceSuperseded` cancel and the new goroutine), then `finish` in `dev.go` |
| `RecSendsTag`, `RecSucceeds`, `RecEndsWithError` | `packedSource.publish`, and the goroutine in `record` that reports its error through `output.problem` |
| `RecTimesOut` | the one-minute `AfterFunc` in `record` (`errSourceUnrecorded`) |
| `shown` | the app's row in `progressOutput` (`progress.go`): `working`, `ready`, `failed` and `problem` all write the row named after the app |
| `SendServed`, `SendRefused`, `Restart` | the runtime's supervisor: `reload` answers 200 once the new app accepts connections and stops the old app first; `boot` starts from the overlay the session's tag names, and reads that tag only when the process starts |
| `RegistryTags` | the registry applying the `Tag` request that ends `publish` |

The variables `tried` and `triedOk` exist only so that the rules can be written.

## Does a change need this

A change needs the check when this prints anything you cannot rule out:

```sh
git diff 54f44c8 -- pkg/installation/dev.go pkg/installation/dev_reload.go pkg/installation/progress.go
```

Changes to these do not alter anything the model describes: `apply`, `update`, `settle` and rollouts, `generate` and `regenerate`, `poll`, `readSession` and `renew`, `packSource` and `overlayPlan`, the ignore rules, and how a status row is drawn. A change to the runtime's `reload` or `boot`, or to who writes the session's tag, does, although it lives in another repository.

## Run it

Check `DevSaves.tla` with the pstack `/statechart` skill, giving it `DevSaves.cfg` as the config and `view.mjs` as the view. Read `/statechart` for how to run it and how to read its report. It installs its own pinned TLC and Java on first use.

On the sync commit the report lists `ReadyServesLastSave`, `RestartKeepsLastSave`, `FailureIsCurrent` and `NoDeadlock` under "Rules that hold", after 21,921 distinct states, in about ten seconds.

## Check that it can still fail

Each config in `faults/` sets `Fault` to one deliberate break of the code. Run each the same way and confirm that the rules named here break:

| Config | The code edit it stands for | Rules that must break |
| --- | --- | --- |
| `faults/keep_sent_on_failure.cfg` | `reloadApp` no longer deletes `d.sent` after a failed `send` | `FailureIsCurrent` |
| `faults/no_supersede.cfg` | `record` no longer cancels the previous recording | `RestartKeepsLastSave`, `ReadyServesLastSave` |
| `faults/silent_unrecorded.cfg` | the goroutine in `record` no longer reports `errSourceUnrecorded` | `RestartKeepsLastSave`, `ReadyServesLastSave` |
| `faults/drop_when_busy.cfg` | `devWatcher.run` offers a change only at the moment the debounce fires, and drops it when the loop is busy | all three |

Run the faults after any edit to the model, and whenever a pass surprises you.

## Open findings

Each config in `findings/` breaks on the sync commit. These are not faults: they describe the code and its surroundings as they are. Run them too. One that stops breaking means the code or the model changed, and you must find out which.

| Config | What it shows | Kind |
| --- | --- | --- |
| `findings/refusal_replaced_by_warning.cfg` | `RefusalStaysShown` breaks. A save is served and its upload is still running. The next save is refused, and the app is shown failed. The upload then fails or passes its minute, and its report replaces the failure with "serves this save, but a restart loses it". | A defect in the CLI: `fail` and the upload's `problem` write the same row. Replayed against the real loop with a scratch test. |
| `findings/cancelled_tag_lands.cfg` | With `CancelledTagMayLand`, `RestartKeepsLastSave` breaks. An upload has sent its tag request when a newer save cancels it. The newer upload moves the tag, and then the older request is applied. The tag names the older save and nothing is reported. | A limit of the design: nothing orders two tag requests, and `record` does not wait for the upload it cancels. It needs the registry to apply the older request after the newer one. |
| `findings/restart_while_uploading.cfg` | With `RestartWhileUploading`, `ReadyServesLastSave` breaks. A save is served and shown ready, and the pod restarts before the upload moves the tag. The app boots from the older save and stays there until the next save. | A limit of the design: the runtime reads the tag only when it starts, and the CLI is not told about a restart. |

When the code closes a finding, move its rule or its setting into `DevSaves.cfg`, delete the finding's config and its row, and say so in the change.

## Read a result

- **The three rules hold and the findings break as listed.** Nothing moved. Say so with the bounds, not as "the dev loop is correct".
- **A rule in `DevSaves.cfg` breaks.** The report gives the shortest path as numbered steps. Replay it against the real loop, one step for each action, as a scratch test beside `TestDevSaves` in `pkg/installation/dev_reload_test.go` (its `reloadFixture` can refuse a save with `failNext` and hold an upload with `unsaved`).
  - If a step cannot happen in the code, the model is wrong. Fix the model and say what was wrong.
  - If every step can happen, it is a bug. Report the path in the code's words, the harm from the table above, and the commit that introduced it. Do not weaken the rule, the bounds or the model, and do not commit the scratch test.

## Change the model with the code

When a change to the covered code is intended, update the model from the code before you run it, never from what makes it pass.

- The loop is one goroutine: keep its steps in order, one action for each point where it waits on the network or a channel. The watcher and each upload are their own goroutines, and each of their steps is its own action.
- A request and its effect on the other side are two actions whenever the CLI can stop waiting in between, as with the tag request.
- If the change adds a check that guards a rule, add a `Fault` value that removes it and a config in `faults/`, and add its row above.
- Change a rule only when the code's promise changed on purpose, and quote the evidence.

After the model passes, its faults fail as listed and its findings break as listed, update the tables here and set the sync commit to the commit you checked. Move it only then. When covered code has changed and nobody rechecked it, use `/pstack:maintain-formal-verification-skill`.

## Bounds and assumptions

A pass covers this much and no more.

- One source app in a session that stays ready, two contents for its source, four saves.
- A save reaches the watcher, and `pack` reads the source at one instant.
- A source request and its reply are one step, and the platform delivers source requests in the order the CLI sends them.
- A tag request the CLI cancelled never reaches the registry afterwards. `findings/cancelled_tag_lands.cfg` drops this.
- The pod does not restart between a save being served and its upload ending. `findings/restart_while_uploading.cfg` drops this.
- A restarted pod runs the tagged overlay before it takes any source request, and the app process does not crash on its own.

Not covered: generated clients and the second reload that `regenerate` makes, apps that require other apps, changes to `holon.yaml` and the watcher that replaces the old one, rollouts and lifecycle hooks, the lease, more than one app, and which files a save counts.
