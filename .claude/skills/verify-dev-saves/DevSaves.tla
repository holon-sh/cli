------------------------------- MODULE DevSaves -------------------------------
\* One source app in a ready holon dev session: the person saves, the watcher
\* debounces, the loop packs and sends the source, and a background goroutine
\* records it in the registry so that a restarted app still runs it. The code is
\* pkg/installation/dev.go and dev_reload.go. Every action names what it stands
\* for. The runtime and the registry are the environment, described from the
\* platform and runtime repositories at the commits SKILL.md names.
EXTENDS Naturals

CONSTANTS
    Contents,             \* what the source can hold; a save changes it to another
    Start,                \* the source the session started from, already recorded
    MaxSaves,             \* saves the person makes
    Fault,                \* "none", or one deliberate break of the code (see SKILL.md)
    CancelledTagMayLand,  \* a tag request the CLI cancelled can still reach the registry
    RestartWhileUploading \* the app's pod can restart before a save is recorded

VARIABLES
    disk,     \* the app's source on disk
    saves,    \* saves made so far
    \* devWatcher.run
    pending,  \* a change the loop has not taken yet
    armed,    \* the debounce timer is running
    offer,    \* the watcher offers pending on its changes channel
    \* devLoop
    pc,       \* where the loop is: select, pack, send, record, finish
    packed,   \* the source the loop last packed
    sent,     \* d.sent for the app: the overlay the loop believes is served, or "none"
    shown,    \* the app's status line: working, ready, failed, unrecorded
    \* devLoop.record: one goroutine per sent overlay
    rec, gen,
    \* The environment
    served,   \* what the runtime serves, or "none"
    tag,      \* the overlay the registry tag for this session names
    wire,     \* recordings whose tag request has left and has not been applied
    \* What the rules are written against. The code has no such fields.
    tried, triedOk

vars == <<disk, saves, pending, armed, offer, pc, packed, sent, shown, rec, gen,
          served, tag, wire, tried, triedOk>>

Ids == 1..MaxSaves

Init ==
    /\ disk = Start
    /\ saves = 0
    /\ pending = FALSE
    /\ armed = FALSE
    /\ offer = FALSE
    /\ pc = "select"
    /\ packed = Start
    /\ sent = Start
    /\ shown = "ready"
    /\ rec = [i \in Ids |-> [content |-> Start, stage |-> "unused", cancel |-> "none"]]
    /\ gen = 0
    /\ served = Start
    /\ tag = Start
    /\ wire = {}
    /\ tried = Start
    /\ triedOk = TRUE

InFlight(i) == rec[i].stage \in {"uploading", "tag_sent"}

----------------------------------------------------------------------------
\* The person and devWatcher.run (dev.go).

\* The person saves the file. fsnotify reports it, and run marks the app's
\* context as pending and restarts the debounce timer.
Save(c) ==
    /\ saves < MaxSaves
    /\ c # disk
    /\ disk' = c
    /\ saves' = saves + 1
    /\ pending' = TRUE
    /\ armed' = TRUE
    /\ UNCHANGED <<offer, pc, packed, sent, shown, rec, gen, served, tag, wire, tried, triedOk>>

\* case <-debounce: the watcher starts to offer what is pending.
DebounceFires ==
    /\ armed
    /\ armed' = FALSE
    /\ IF Fault = "drop_when_busy" /\ pc # "select"
         THEN pending' = FALSE /\ UNCHANGED offer
         ELSE offer' = (offer \/ pending) /\ UNCHANGED pending
    /\ UNCHANGED <<disk, saves, pc, packed, sent, shown, rec, gen, served, tag, wire, tried, triedOk>>

----------------------------------------------------------------------------
\* devLoop.run, rebuild, reload, reloadApp, send (dev.go, dev_reload.go).

\* case change := <-changes in run: the loop takes the change.
LoopTakes ==
    /\ pc = "select"
    /\ offer
    /\ offer' = FALSE
    /\ pending' = FALSE
    /\ pc' = "pack"
    /\ UNCHANGED <<disk, saves, armed, packed, sent, shown, rec, gen, served, tag, wire, tried, triedOk>>

\* reloadApp packs the source and skips an overlay equal to d.sent.
Pack ==
    /\ pc = "pack"
    /\ packed' = disk
    /\ IF disk = sent
         THEN pc' = "select" /\ UNCHANGED shown
         ELSE pc' = "send" /\ shown' = "working"
    /\ UNCHANGED <<disk, saves, pending, armed, offer, sent, rec, gen, served, tag, wire, tried, triedOk>>

\* send: the platform answers 200 once the runtime serves the overlay.
SendServed ==
    /\ pc = "send"
    /\ served' = packed
    /\ sent' = packed
    /\ tried' = packed
    /\ triedOk' = TRUE
    /\ pc' = "record"
    /\ UNCHANGED <<disk, saves, pending, armed, offer, packed, shown, rec, gen, tag, wire>>

\* send fails: the runtime could not be reached, or it stopped the old app and
\* the new one did not start. reloadApp deletes d.sent and reload calls fail.
SendRefused ==
    /\ pc = "send"
    /\ served' \in {served, "none"}
    /\ sent' = IF Fault = "keep_sent_on_failure" THEN sent ELSE "none"
    /\ shown' = "failed"
    /\ tried' = packed
    /\ triedOk' = FALSE
    /\ pc' = "select"
    /\ UNCHANGED <<disk, saves, pending, armed, offer, packed, rec, gen, tag, wire>>

\* record cancels the previous recording as superseded and starts a new one.
Record ==
    /\ pc = "record"
    /\ gen' = gen + 1
    /\ LET superseded(i) == i = gen /\ InFlight(i) /\ rec[i].cancel = "none"
                              /\ Fault # "no_supersede" IN
         /\ rec' = [i \in Ids |->
                      IF i = gen + 1
                        THEN [content |-> sent, stage |-> "uploading", cancel |-> "none"]
                        ELSE IF superseded(i) THEN [rec[i] EXCEPT !.cancel = "superseded"]
                        ELSE rec[i]]
         /\ wire' = IF CancelledTagMayLand THEN wire
                    ELSE {i \in wire : ~superseded(i)}
    /\ pc' = "finish"
    /\ UNCHANGED <<disk, saves, pending, armed, offer, packed, sent, shown, served, tag, tried, triedOk>>

\* regenerate has run, and finish calls output.ready, which leaves an error on
\* the app's row.
Finish ==
    /\ pc = "finish"
    /\ shown' = IF shown = "unrecorded" /\ Fault # "ready_over_warning" THEN shown ELSE "ready"
    /\ pc' = "select"
    /\ UNCHANGED <<disk, saves, pending, armed, offer, packed, sent, rec, gen, served, tag,
                   wire, tried, triedOk>>

----------------------------------------------------------------------------
\* The goroutine devLoop.record starts, running packedSource.publish.

\* publish has pushed the blob and the manifest and sends the tag request.
RecSendsTag(i) ==
    /\ rec[i].stage = "uploading"
    /\ rec[i].cancel = "none"
    /\ rec' = [rec EXCEPT ![i].stage = "tag_sent"]
    /\ wire' = wire \cup {i}
    /\ UNCHANGED <<disk, saves, pending, armed, offer, pc, packed, sent, shown, gen,
                   served, tag, tried, triedOk>>

\* The registry points the session's tag at this recording's overlay.
RegistryTags(i) ==
    /\ i \in wire
    /\ tag' = rec[i].content
    /\ wire' = wire \ {i}
    /\ UNCHANGED <<disk, saves, pending, armed, offer, pc, packed, sent, shown, rec, gen,
                   served, tried, triedOk>>

\* publish returns nil: the registry answered the tag request.
RecSucceeds(i) ==
    /\ rec[i].stage = "tag_sent"
    /\ i \notin wire
    /\ rec[i].cancel = "none"
    /\ rec' = [rec EXCEPT ![i].stage = "ended"]
    /\ UNCHANGED <<disk, saves, pending, armed, offer, pc, packed, sent, shown, gen,
                   served, tag, wire, tried, triedOk>>

\* The one-minute AfterFunc cancels the recording with errSourceUnrecorded.
RecTimesOut(i) ==
    /\ InFlight(i)
    /\ rec[i].cancel = "none"
    /\ rec' = [rec EXCEPT ![i].cancel = "unrecorded"]
    /\ wire' = IF CancelledTagMayLand THEN wire ELSE wire \ {i}
    /\ UNCHANGED <<disk, saves, pending, armed, offer, pc, packed, sent, shown, gen,
                   served, tag, tried, triedOk>>

\* publish returns an error: a failed request, or the cancelled context. Unless
\* the cause is errSourceSuperseded the goroutine calls output.unrecorded, which
\* leaves an error on the app's row.
RecEndsWithError(i) ==
    /\ InFlight(i)
    /\ rec' = [rec EXCEPT ![i].stage = "ended"]
    /\ wire' = IF CancelledTagMayLand THEN wire ELSE wire \ {i}
    /\ LET silent == \/ rec[i].cancel = "superseded"
                     \/ rec[i].cancel = "unrecorded" /\ Fault = "silent_unrecorded"
                     \/ shown = "failed" /\ Fault # "warn_over_any_status" IN
         shown' = IF silent THEN shown ELSE "unrecorded"
    /\ UNCHANGED <<disk, saves, pending, armed, offer, pc, packed, sent, gen,
                   served, tag, tried, triedOk>>

----------------------------------------------------------------------------
\* The runtime (host/supervisor.go in the runtime repository).

Uploading == pc = "record" \/ wire # {} \/ \E i \in Ids : InFlight(i)

\* The pod or its container restarts, and boot starts the app from the tag.
Restart ==
    /\ RestartWhileUploading \/ ~Uploading
    /\ pc # "send"
    /\ served # tag
    /\ served' = tag
    /\ UNCHANGED <<disk, saves, pending, armed, offer, pc, packed, sent, shown, rec, gen,
                   tag, wire, tried, triedOk>>

----------------------------------------------------------------------------

\* Every save is made and nothing is in motion: the end of a bounded run.
Settled ==
    /\ saves = MaxSaves
    /\ pc = "select" /\ ~pending /\ ~armed /\ ~offer
    /\ UNCHANGED vars

Next ==
    \/ \E c \in Contents : Save(c)
    \/ DebounceFires
    \/ LoopTakes
    \/ Pack
    \/ SendServed
    \/ SendRefused
    \/ Record
    \/ Finish
    \/ \E i \in Ids :
            \/ RecSendsTag(i)
            \/ RegistryTags(i)
            \/ RecSucceeds(i)
            \/ RecTimesOut(i)
            \/ RecEndsWithError(i)
    \/ Restart
    \/ Settled

Spec == Init /\ [][Next]_vars

----------------------------------------------------------------------------
\* The rules. Quiet is the loop waiting in select with nothing for it to take.

Quiet == pc = "select" /\ ~pending /\ ~armed /\ ~offer

RecordingsEnded == wire = {} /\ \A i \in Ids : ~InFlight(i)

\* An app shown ready serves the last save.
ReadyServesLastSave == Quiet /\ shown = "ready" => served = disk

\* An app shown ready, once its uploads have ended, restarts into the last save.
RestartKeepsLastSave == Quiet /\ shown = "ready" /\ RecordingsEnded => tag = disk

\* An app shown failed is one whose current source was sent and refused.
FailureIsCurrent == Quiet /\ shown = "failed" => tried = disk /\ ~triedOk

\* An app whose current source was refused stays shown as failed.
RefusalStaysShown == Quiet /\ ~triedOk => shown = "failed"

=============================================================================
