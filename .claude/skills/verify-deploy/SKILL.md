---
name: verify-deploy
description: "Model-check that every release holon deploy publishes runs as it was built, with a TLA+ model of the build pod, the image the CLI assembles (pkg/installation/freeze.go and build.go, pkg/sourcebuild/image.go) and the registry, while deploys overlap, build pods restart, the platform is upgraded and the registry collects untagged content. Use when a change touches buildSource or sourcebuild.Assemble, the runtime's freeze, HOLON_BASE_IMAGE, the base-copy hook, registry maintenance, or how a source app's pod picks its image."
---

# Verify that holon deploy releases what it built

`holon deploy` sends the source to a build pod, which installs, builds and pushes the app's layers in a manifest naming the development image it runs. The CLI mounts that development image's layers into the app's repository, appends the app's layers, and tags the release. A platform upgrade, a pod restart, a registry collection and a second deploy of the same app can all happen in between. `Deploy.tla` is a TLA+ model of two deploys of one app by one person, and TLC checks every order of those events within the bounds below.

## The rule

| Rule | What it says | What breaks for the person when it fails |
| --- | --- | --- |
| `ReleasesRunAsBuilt` | Every release is in the app's repository with every layer it names, and runs on the development image its layers were built on. | Production cannot pull the release, or runs it on an image its dependencies were not installed for, and the app does not start. |

It holds on the sync commits. Promises that need no interleaving are pinned by Go tests, not here: `TestRunPreparationFailures` checks that a failed type check or a missing development image fails the deploy with its own error, and that every failed deploy deletes its build session.

## What the model describes

Sync commits: CLI `dcabb0e` (hughhan1/cli-private#49), runtime `17a0898` (hughhan1/runtime-private#11), holon `077186e6` (holon-sh/holon#434), `a3d496e3` (holon#435) and main `d54b3963`, protocol `c2efee5`.

| Actions | Code |
| --- | --- |
| `Start` | `buildSession.start`, whose pod runs the controller's `DEVELOPMENT_IMAGE` (`Config.image`), and `buildSource`'s build call |
| `Freeze` | the runtime's `freeze`: `HEAD`, upload what the repository lacks, then a manifest whose base annotation is `HOLON_BASE_IMAGE` |
| `Answer` | the pod's reply after a passing type check |
| `Mount` | `sourcebuild.Assemble`: the base digest from the frozen manifest, its index from `development`, then `ociimage.Edit`, which mounts the base layers before it pushes anything |
| `Publish` | `ociimage.Edit`'s push of the config and manifest, which the registry refuses unless the repository holds every layer, and `appartifact.Publish` |
| `Stop` | any error, refusal, failed check, Ctrl-C or kill; the pod's build ends with the request |
| `ControllerUpgrades`, `HookCopies` | a Helm upgrade rolling the controller, and the `holon-registry-base` hook tagging the new image `holon-retained` |
| `PodRestarts` | a build pod restarting on the controller's image; holon-api's `reach` retries the call for ten seconds |
| `Collect`, `Mark`, `Sweep` | `maintainRegistry`: holon-api and the registry scaled to zero, then `garbage-collect --delete-untagged`; `Mark` and `Sweep` only under `gc_while_serving` |

## Does a change need this

A change needs the check when any of these prints anything you cannot rule out.

```sh
git diff 0d410a8 -- pkg/installation/freeze.go pkg/installation/build.go pkg/sourcebuild/image.go
```

```sh
git diff 17a0898 -- host/overlay.go
```

In holon, from the sync commit your branch holds:

```sh
git diff d54b3963 -- internal/registry internal/controller/config.go internal/controller/manifests.go internal/controller/registry_maintenance.go charts/holon/templates/registry-base.yaml charts/holon/templates/runtime-pod-guardrails.yaml
```

## Run it

Check `Deploy.tla` with the pstack `/statechart` skill, giving it `Deploy.cfg` and `view.mjs`. On the sync commits `ReleasesRunAsBuilt` and `NoDeadlock` hold after 4,335 distinct states.

Each config in `faults/` sets `Fault` to one deliberate break, and `ReleasesRunAsBuilt` must break under each:

| Config | The code edit it stands for |
| --- | --- |
| `faults/gc_while_serving.cfg` | `maintainRegistry` runs the collection without scaling holon-api and the registry to zero |
| `faults/assemble_on_retained.cfg` | `sourcebuild.Assemble` assembles on the image tagged `holon-retained` instead of the frozen manifest's base |

When the rule breaks on the code, replay the path against the real code, the CLI's steps as a scratch test beside `TestRunDeployLifecycle` (the CLI builds only with `GOWORK` naming a protocol checkout at the commit above). If a step cannot happen, fix the model; if every step can, it is a bug: report it with the harm and the commit, and do not weaken the rule. Update the model from the code before you run it, and move the sync commits only after the rule holds and both faults break.

## Bounds and assumptions

- Two deploys of one app by one person, sharing its repository; a person and CI deploy into two repositories and share nothing here. One upgrade, one registry collection, one build pod restart; any deploy may stop at any step.
- The registry refuses a manifest whose layers the repository lacks (distribution 3.1.1), and a collection removes what no tag reaches. A dependency layer of one lockfile on one image has one digest.
- The pod's `HOLON_BASE_IMAGE` is the image it runs: the controller sets both from one value, and the pod guardrail admits no other.
- Reading the base and mounting it are one step, as are pushing the image and tagging it: a collection between them only stops the deploy.

Not covered: apps that require other apps, more than one app per deploy, time, production's rollout, and the single functions with no interleaving (inspection's top-down walk, the runtime's layer split, the recipe table), which want bounded exhaustive tests against the real code.
