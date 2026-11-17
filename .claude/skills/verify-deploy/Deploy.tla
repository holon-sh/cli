-------------------------------- MODULE Deploy --------------------------------
\* holon deploy of one source app, from the build call to the release tag, for
\* deploys of the same app by one person that share its repository. The CLI
\* code is pkg/installation/freeze.go and build.go; the build pods, the
\* controller and the registry are described from the commits SKILL.md names.
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS
    Deploys,         \* deploys of the app that run at once, by one person
    Old, New,        \* the development image before and after a platform upgrade
    MaxCollections,  \* registry maintenance runs
    MaxRestarts,     \* build pod restarts, the upgrade's roll included
    Fault            \* "none", or one deliberate break of the code (see SKILL.md)

VARIABLES
    pc,           \* where each deploy is
    call,         \* its build call at the pod: none, building, frozen
    pod,          \* the development image its build pod runs, or "none"
    assembledOn,  \* the development image assembleImage assembles on, or "none"
    ctrlImage,    \* the controller's DEVELOPMENT_IMAGE, which a new build pod runs
    base,         \* development images the repository development holds
    tagged,       \* the development image tagged holon-retained
    blobs,        \* layers the app's repository holds
    released,     \* images a release tag names
    marked,       \* Fault gc_while_serving only: what a running collection marked
    collections, restarts

vars == <<pc, call, pod, assembledOn, ctrlImage, base, tagged, blobs, released, marked, collections, restarts>>

None == "none"
Symmetry == Permutations(Deploys)

\* A layer is <<kind, the development image it was built on or is, the deploy
\* whose source it holds>>. The dependency layer of one lockfile on one image
\* is the same bytes in every deploy, so it carries no deploy.
BaseLayers(b) == <<"base", b, None>>
Frozen(d, on) == {<<"dependencies", on, None>>, <<"source", on, d>>}
Needs(i) == {BaseLayers(i.base)} \cup Frozen(i.deploy, i.on)
Assembled(d) == [deploy |-> d, base |-> assembledOn[d], on |-> pod[d]]
Kept == UNION {Needs(i) : i \in released}
Ended == {"done", "stopped"}

Init ==
    /\ pc = [d \in Deploys |-> "start"]
    /\ call = [d \in Deploys |-> None]
    /\ pod = [d \in Deploys |-> None]
    /\ assembledOn = [d \in Deploys |-> None]
    /\ ctrlImage = Old
    /\ base = {Old}
    /\ tagged = Old
    /\ blobs = {}
    /\ released = {}
    /\ marked = {}
    /\ collections = 0
    /\ restarts = 0

----------------------------------------------------------------------------
\* The CLI and the build pod.

\* buildSession.start creates the session, whose pod runs the controller's
\* image, and buildSource sends the build call: the pod installs and builds.
Start(d) ==
    /\ pc[d] = "start"
    /\ pc' = [pc EXCEPT ![d] = "building"]
    /\ call' = [call EXCEPT ![d] = "building"]
    /\ pod' = [pod EXCEPT ![d] = ctrlImage]
    /\ UNCHANGED <<assembledOn, ctrlImage, base, tagged, blobs, released, marked, collections, restarts>>

\* The runtime's freeze pushes the layers the repository lacks (HEAD, then
\* upload), then an untagged manifest whose base annotation names the image the
\* pod runs.
Freeze(d) ==
    /\ call[d] = "building"
    /\ call' = [call EXCEPT ![d] = "frozen"]
    /\ blobs' = blobs \cup Frozen(d, pod[d])
    /\ UNCHANGED <<pc, pod, assembledOn, ctrlImage, base, tagged, released, marked, collections, restarts>>

\* The type check passes and the pod answers with the frozen manifest's digest.
Answer(d) ==
    /\ pc[d] = "building"
    /\ call[d] = "frozen"
    /\ pc' = [pc EXCEPT ![d] = "mount"]
    /\ call' = [call EXCEPT ![d] = None]
    /\ UNCHANGED <<pod, assembledOn, ctrlImage, base, tagged, blobs, released, marked, collections, restarts>>

\* assembleImage takes the development image from the frozen manifest's base
\* annotation, reads its index from development and mounts its layers into
\* the app's repository.
Mount(d) ==
    /\ pc[d] = "mount"
    /\ LET b == IF Fault = "assemble_on_retained" THEN tagged ELSE pod[d] IN
         /\ b \in base
         /\ assembledOn' = [assembledOn EXCEPT ![d] = b]
         /\ blobs' = blobs \cup {BaseLayers(b)}
    /\ pc' = [pc EXCEPT ![d] = "publish"]
    /\ UNCHANGED <<call, pod, ctrlImage, base, tagged, released, marked, collections, restarts>>

\* ociimage.Edit pushes the image, which the registry refuses unless the
\* repository holds every layer it names, and appartifact.Publish tags the
\* release.
Publish(d) ==
    /\ pc[d] = "publish"
    /\ Needs(Assembled(d)) \subseteq blobs
    /\ pc' = [pc EXCEPT ![d] = "done"]
    /\ released' = released \cup {Assembled(d)}
    /\ UNCHANGED <<call, pod, assembledOn, ctrlImage, base, tagged, blobs, marked, collections, restarts>>

\* The deploy stops: a failed or cut request, a failed contract or type check,
\* a base the registry no longer holds, a refusal, Ctrl-C, or a killed CLI.
\* The pod's build ends with the request.
Stop(d) ==
    /\ pc[d] \notin Ended \cup {"start"}
    /\ pc' = [pc EXCEPT ![d] = "stopped"]
    /\ call' = [call EXCEPT ![d] = None]
    /\ UNCHANGED <<pod, assembledOn, ctrlImage, base, tagged, blobs, released, marked, collections, restarts>>

----------------------------------------------------------------------------
\* The environment.

\* A Helm upgrade that changes the pinned development image rolls the
\* controller; build pods move to the new image when they restart.
ControllerUpgrades ==
    /\ ctrlImage = Old
    /\ ctrlImage' = New
    /\ UNCHANGED <<pc, call, pod, assembledOn, base, tagged, blobs, released, marked, collections, restarts>>

\* The post-upgrade hook copies the new image into development and tags it
\* holon-retained, which leaves the old one untagged.
HookCopies ==
    /\ tagged = Old
    /\ base' = base \cup {New}
    /\ tagged' = New
    /\ UNCHANGED <<pc, call, pod, assembledOn, ctrlImage, blobs, released, marked, collections, restarts>>

\* A build pod restarts on the controller's image. holon-api's reach retries
\* the cut call for ten seconds, so it starts again on the new pod or is cut.
PodRestarts(d) ==
    /\ call[d] # None
    /\ restarts < MaxRestarts
    /\ restarts' = restarts + 1
    /\ pod' = [pod EXCEPT ![d] = ctrlImage]
    /\ \/ call' = [call EXCEPT ![d] = "building"]
       \/ call' = [call EXCEPT ![d] = None]
    /\ UNCHANGED <<pc, assembledOn, ctrlImage, base, tagged, blobs, released, marked, collections>>

\* Registry maintenance scales holon-api and the registry to zero, which cuts
\* every build call, then garbage-collect --delete-untagged removes what no tag
\* reaches.
Collect ==
    /\ Fault # "gc_while_serving"
    /\ collections < MaxCollections
    /\ collections' = collections + 1
    /\ blobs' = blobs \cap Kept
    /\ base' = base \cap {tagged}
    /\ call' = [d \in Deploys |-> None]
    /\ UNCHANGED <<pc, pod, assembledOn, ctrlImage, tagged, released, marked, restarts>>

\* Fault gc_while_serving: the collection marks and sweeps while the registry
\* takes pushes.
Mark ==
    /\ Fault = "gc_while_serving"
    /\ collections < MaxCollections
    /\ collections' = collections + 1
    /\ marked' = {Kept}
    /\ UNCHANGED <<pc, call, pod, assembledOn, ctrlImage, base, tagged, blobs, released, restarts>>

Sweep ==
    /\ \E kept \in marked : blobs' = blobs \cap kept
    /\ base' = base \cap {tagged}
    /\ marked' = {}
    /\ UNCHANGED <<pc, call, pod, assembledOn, ctrlImage, tagged, released, collections, restarts>>

Finished ==
    /\ \A d \in Deploys : pc[d] \in Ended
    /\ UNCHANGED vars

Next ==
    \/ \E d \in Deploys :
        Start(d) \/ Freeze(d) \/ Answer(d) \/ Mount(d) \/ Publish(d) \/ Stop(d) \/ PodRestarts(d)
    \/ ControllerUpgrades \/ HookCopies \/ Collect \/ Mark \/ Sweep \/ Finished

Spec == Init /\ [][Next]_vars

----------------------------------------------------------------------------

\* Every release is in its repository whole and runs on the development image
\* its layers were built on.
ReleasesRunAsBuilt ==
    \A i \in released : Needs(i) \subseteq blobs /\ i.base = i.on

=============================================================================
