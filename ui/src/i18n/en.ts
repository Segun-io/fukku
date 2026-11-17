// The source catalogue: a flat object of "area.key": "text". `es.ts` must have the same keys
// (it is typed against this object, so a missing key does not compile).
//
// - `{name}` is replaced by the parameter of that name.
// - A key can have plural forms: "area.key.one", "area.key.other" (the CLDR categories of the
//   language). `t("area.key", { count })` picks the form for `count`; `.other` is the fallback.
// - Add keys under the area of the page or component that uses them (shell, upload, jobs,
//   job, state, errors). #8 adds `shell.*` and `patterns.*`.
const en = {
  "shell.skip": "Skip to content",
  "shell.language": "Language",
  "shell.nav.label": "Main",
  "shell.nav.design": "Patterns",
  "shell.loading": "Loading…",
  "shell.insecure.title": "Authentication is off",
  "shell.insecure.body":
    "This server runs in insecure development mode: anyone who can reach it is an administrator. Never use it with real files.",
  "shell.offline.title": "No connection",
  "shell.offline.body":
    "Uploads wait and continue by themselves when the connection is back. Keep this page open.",
  "shell.signedOut.title": "Sign in again",
  "shell.signedOut.body":
    "Your session ended. Nothing is lost: uploads continue from where they stopped once you are signed in.",
  "shell.signedOut.action": "Sign in again",
  "shell.signedOut.hint": "It opens in a new tab. Come back to this one afterwards.",
  "shell.error.title": "This page could not load",
  "shell.error.body": "The server did not answer. Check the connection, then try again.",
  "shell.error.retry": "Try again",
  "shell.error.detail": "Technical detail",
  "shell.notFound.title": "This page does not exist",
  "shell.notFound.body": "The address may be mistyped, or what it showed was deleted.",
  "shell.notFound.action": "Go to the start page",

  // ---- patterns: file progress ---------------------------------------------------------
  "patterns.progress.of": "{sent} of {total}",
  "patterns.progress.left": "about {time} left",
  "patterns.progress.valueText": "{percent} uploaded",
  "patterns.progress.status.queued": "Waiting its turn",
  "patterns.progress.status.uploading": "Uploading",
  "patterns.progress.status.paused": "Paused",
  "patterns.progress.status.reconnecting": "Waiting for the connection",
  "patterns.progress.status.failed": "Upload stopped",
  "patterns.progress.status.done": "Uploaded",
  "patterns.progress.status.unfinished": "Not finished",
  "patterns.progress.hint.paused": "Stopped at {sent}. It continues from there.",
  "patterns.progress.hint.reconnecting": "It continues by itself from {sent}.",
  "patterns.progress.hint.unfinished": "{sent} is on the server. It continues from there.",
  "patterns.progress.hint.resumed": "Continued from {sent}. Nothing is sent twice.",
  "patterns.progress.hint.slow":
    "This connection is slow. Keep the page open; if it drops, the upload continues from where it stopped.",
  "patterns.progress.pause": "Pause",
  "patterns.progress.pause.label": "Pause {name}",
  "patterns.progress.resume": "Resume",
  "patterns.progress.resume.label": "Resume {name}",
  "patterns.progress.retry": "Try again",
  "patterns.progress.retry.label": "Try {name} again",
  "patterns.progress.remove": "Remove",
  "patterns.progress.remove.label": "Remove {name}",

  // ---- patterns: status timeline -------------------------------------------------------
  "patterns.timeline.label": "What has happened so far",
  "patterns.flow.label": "Steps of the import",
  "patterns.flow.step.match": "Choose the title",
  "patterns.flow.step.mapping": "Check the files",
  "patterns.flow.step.confirm": "Confirm",
  "patterns.timeline.step.done": "Done",
  "patterns.timeline.step.current": "Now",
  "patterns.timeline.step.upcoming": "Not yet",
  "patterns.timeline.failed": "Stopped here. It needs you: try again, or cancel.",
  "patterns.timeline.stalled":
    "Waiting here for something that did not answer. It continues by itself.",
  "patterns.timeline.cancelled": "Cancelled here.",
  "patterns.state.uploading": "Uploading",
  "patterns.state.uploaded": "Upload complete",
  "patterns.state.probing": "Checking the files",
  "patterns.state.probed": "Files checked",
  "patterns.state.matched": "Title found",
  "patterns.state.awaiting-confirmation": "Waiting for your confirmation",
  "patterns.state.importing": "Adding to the library",
  "patterns.state.imported": "In the library",
  "patterns.state.post-processing": "Finishing: subtitles and the media server",
  "patterns.state.done": "Done",

  // ---- patterns: confirmation ----------------------------------------------------------
  "patterns.confirm.warnings": "Before you confirm",
  "patterns.confirm.ack": "Confirm that you have read this",
  "patterns.confirm.ackMissing": "Tick the box above to continue.",
  "patterns.confirm.working": "Working…",

  // ---- patterns: file list -------------------------------------------------------------
  "patterns.files.label": "Files in this upload",
  "patterns.files.kind.video": "Video",
  "patterns.files.kind.subtitle": "Subtitles",
  "patterns.files.kind.archive": "Archive",
  "patterns.files.status.ok": "Accepted",
  "patterns.files.status.pending": "Not checked yet",
  "patterns.files.status.refused": "Refused",
  "patterns.files.from": "From {archive}",
  "patterns.files.refusedNote": "A refused file is not kept. The other files are not affected.",

  // ---- the /design page (development builds only) ------------------------------------
  "patterns.demo.title": "Patterns",
  "patterns.demo.intro":
    "The building blocks of the upload and job pages, with fixed sample data. This page exists in development builds only.",
  "patterns.demo.progress.title": "File progress",
  "patterns.demo.progress.live": "A live row, to watch the figures change without moving",
  "patterns.demo.progress.failed": "The server refused the file: it is larger than 80 GB.",
  "patterns.demo.timeline.title": "Status timeline",
  "patterns.demo.timeline.running": "Running",
  "patterns.demo.timeline.failed": "Failed",
  "patterns.demo.timeline.stalled": "Stalled",
  "patterns.demo.timeline.cancelled": "Cancelled",
  "patterns.demo.timeline.done": "Finished",
  "patterns.demo.timeline.failedDetail": "The worker did not start within 10 minutes.",
  "patterns.demo.timeline.stalledDetail": "Radarr is not answering.",
  "patterns.demo.timeline.cancel": "Cancel this upload",
  "patterns.demo.confirm.title": "Confirmation with warnings",
  "patterns.demo.confirm.heading": "Add this film to the library?",
  "patterns.demo.confirm.film": "Film",
  "patterns.demo.confirm.file": "File",
  "patterns.demo.confirm.quality": "Quality",
  "patterns.demo.confirm.audio": "Audio",
  "patterns.demo.confirm.audioValue": "Spanish (Latin America), English",
  "patterns.demo.confirm.warn1":
    "Radarr already has a file for this film. Confirming replaces it; the old file is deleted.",
  "patterns.demo.confirm.warn2":
    "The upload is 1080p and the file in the library is 2160p. Radarr counts this as a downgrade.",
  "patterns.demo.confirm.action": "Add to the library",
  "patterns.demo.confirm.back": "Choose another film",
  "patterns.demo.confirm.result": "Confirmed. On a real page the import starts here.",
  "patterns.demo.files.title": "File list",
  "patterns.demo.files.refused1":
    "No video was found inside this file. It may be damaged or renamed.",
  "patterns.demo.files.refused2":
    "This kind of file is not accepted: only video, subtitles and zip.",
  "patterns.demo.picker.title": "File picker",
  "patterns.demo.picker.full": "Nothing chosen yet",
  "patterns.demo.picker.compact": "With files under it",
  "patterns.demo.copy.title": "Copy field",
  "patterns.demo.select.title": "Select field",
  "patterns.demo.choice.title": "Choice with consequences",
  "patterns.demo.compare.title": "Two things side by side",
  "patterns.demo.nudge.title": "Field with step buttons",
  "patterns.demo.status.title": "State of a row",
  "patterns.demo.flow.title": "Head of a step",
  "patterns.demo.shell.title": "Shell states",
  "patterns.demo.shell.banner": "Insecure-development banner",
  "patterns.demo.shell.offline": "Offline",
  "patterns.demo.shell.signedOut": "Signed out",
  "patterns.demo.shell.empty": "Empty",
  "patterns.demo.shell.error": "Error",
  "patterns.demo.shell.loading": "Loading",
  "patterns.demo.shell.nav": "Header navigation",
  "patterns.demo.empty.title": "Nothing uploaded yet",
  "patterns.demo.empty.body":
    "Films and seasons you upload appear here, with what happens to each one.",
  "patterns.demo.empty.action": "Upload a file",
  "patterns.demo.error.detail": "GET /api/jobs: 502 Bad Gateway",
  "nav.upload": "Upload",
  "nav.jobs": "Your uploads",
  "nav.admin": "Admin",

  // ---- admin: health and settings (read-only) -----------------------------------------
  "admin.title": "Admin",
  "admin.intro":
    "How the server is doing, and the settings it runs with. Nothing here can be changed.",
  "admin.health.title": "Health",
  "admin.health.refresh": "Check again",
  "admin.health.checked": "Checked {date}",
  "admin.health.version": "Fukku {version}",
  "admin.health.error.title": "The health report could not load",
  "admin.status.ready": "Everything works",
  "admin.status.degraded": "Working, but an app does not answer",
  "admin.status.not_ready": "Not ready: something on this server is failing",
  "admin.checks.title": "Checks",
  "admin.check.ok": "Passing",
  "admin.check.failing": "Failing",
  "admin.check.database": "Database",
  "admin.check.spool": "Spool folder",
  "admin.check.sentinels": "Storage mounted",
  "admin.check.intakeRoot": "Intake folder",
  "admin.check.sameFilesystem": "Same filesystem as the library",
  "admin.check.workerHeartbeat": "Worker",
  "admin.check.radarr": "Radarr",
  "admin.check.sonarr": "Sonarr",
  "admin.apps.title": "Apps",
  "admin.apps.version": "Version {version}",
  "admin.apps.noVersion": "No version: the app did not answer",
  "admin.apps.none": "Radarr and Sonarr are not set up.",
  "admin.worker.title": "Worker",
  "admin.worker.age": "Last heartbeat {age} ago. It is a problem after {max}.",
  "admin.worker.none":
    "No heartbeat. The worker is not running, or cannot write to the spool folder.",
  "admin.jobs.title": "Uploads by state",
  "admin.jobs.total.one": "{count} upload in total, from everyone",
  "admin.jobs.total.other": "{count} uploads in total, from everyone",
  "admin.jobs.none": "The uploads could not be counted.",
  "admin.disk.title": "Disk space",
  "admin.disk.free": "{free} free in the intake folder",
  "admin.disk.above": "That is above the {margin} kept free, so uploads are accepted.",
  "admin.disk.below": "That is below the {margin} kept free, so new uploads are refused.",
  "admin.disk.unknown": "The free space cannot be measured on this system. The margin is {margin}.",
  "admin.settings.title": "Settings",
  "admin.settings.intro":
    "Read from the configuration file and the environment. To change one, edit those and restart. Secrets are never shown.",
  "admin.settings.onlySet": "Only settings that are not the default",
  "admin.settings.error.title": "The settings could not load",
  "admin.settings.none": "Every setting has its default value.",
  "admin.setting.default": "Default",
  "admin.setting.set": "Set",
  "admin.setting.empty": "Empty",
  "admin.setting.hidden": "Set, hidden",
  "admin.setting.notSet": "Not set",
  "admin.setting.env": "Environment: {name}",

  "upload.title": "Upload",
  "upload.intro":
    "Choose the film, the show or the subtitle files to send. Big files are fine: if the connection drops or you reload the page, the upload continues from where it stopped.",
  "upload.restoring": "Checking your unfinished upload…",
  "upload.drop.title": "Choose your files",
  "upload.drop.hint": "Pick several files, or one zip, or drop them here.",
  "upload.drop.accepted": "Accepted: {types}",
  "upload.choose": "Choose files",
  "upload.files.title": "Your files",
  "upload.summary.one": "{done} of {count} file uploaded",
  "upload.summary.other": "{done} of {count} files uploaded",
  "upload.start": "Start upload",
  "upload.starting": "Starting…",
  "upload.submitting": "Every file arrived. Finishing the upload…",
  "upload.submitFailed.title": "The files arrived, but the upload could not be finished",
  "upload.discard": "Discard this upload",
  "upload.pausedSignedOut": "Paused until you sign in again. It continues from where it stopped.",
  "upload.discard.confirm": "Discard this upload? What was already sent is deleted.",
  "upload.startOver": "Start over",
  "upload.remove.confirm": "Remove {name}? What was already sent is deleted.",
  "upload.rejected.title": "Some files were not added",
  "upload.rejected.dismiss": "Hide this list",
  "upload.reason.file_mismatch":
    "This is not the file that was being uploaded: its name, size or date differ. Choose the original file.",
  "upload.resume.title": "Your upload was interrupted",
  "upload.resume.body":
    "Choose the same files again. Each one continues from where the server has it, and nothing is sent twice.",
  "upload.large.title": "Large file",
  "upload.large.body":
    "Very large files upload best from home or another fast connection. If you must use this one, keep this page open; if it drops, the upload continues from where it stopped.",
  "upload.jobGone.title": "This upload cannot continue",
  "upload.jobGone.body": "{reason} Start over to send the files again.",

  "jobs.title": "Your uploads",
  "jobs.list": "Uploads",
  "jobs.count.one": "{count} upload",
  "jobs.count.other": "{count} uploads",
  "jobs.files.one": "{count} file",
  "jobs.files.other": "{count} files",
  "jobs.unnamed": "Untitled upload",
  "jobs.filter.state": "State",
  "jobs.filter.allStates": "All states",
  "jobs.filter.scope": "Show",
  "jobs.scope.mine": "Mine",
  "jobs.scope.everyone": "Everyone",
  "jobs.owner": "By {owner}",
  "jobs.started": "Started {age}",
  "jobs.expires": "Deleted on {date}",
  "jobs.empty.title": "Nothing uploaded yet",
  "jobs.empty.body": "Films and seasons you upload appear here, with what happens to each one.",
  "jobs.empty.action": "Upload a file",
  "jobs.emptyFiltered.title": "No uploads match",
  "jobs.emptyFiltered.body": "Try another state, or show all of them.",
  "jobs.more": "Show more",
  "jobs.error.title": "Your uploads could not load",

  "job.title": "Upload",
  "job.back": "Back to your uploads",
  "job.notFound.title": "This upload was not found",
  "job.notFound.body": "It may have been deleted, or it may belong to someone else.",
  "job.error.title": "This upload could not load",
  "job.owner": "Uploaded by {owner}",
  "job.size": "Size: {size}",
  "job.started": "Started {date}",
  "job.expires": "Kept until {date}, then deleted automatically.",
  "job.progress": "Progress",
  "job.files": "Files",
  "job.files.none": "No files yet.",
  "job.uploads.title": "Unfinished uploads",
  "job.uploads.item": "{done} of {total}",
  "job.uploads.hint":
    "Choose the same files again on the Upload page and they continue from where they stopped.",
  "job.uploads.continue": "Continue uploading",
  "job.probe.title": "What was found in {name}",
  "job.probe.duration": "Duration",
  "job.probe.video": "Video",
  "job.probe.audio": "Audio",
  "job.probe.subtitles": "Subtitles",
  "job.probe.none": "None",
  "job.probe.noLanguage": "Language not tagged",
  "job.probe.rate": "{rate} fps",
  "job.probe.flag.default": "default",
  "job.probe.flag.forced": "forced",
  "job.probe.flag.hearingImpaired": "for the hard of hearing",
  "job.skipped.title": "Left out of {name}",
  "job.skipped.note": "These items were not taken out. Everything else was.",
  "job.skipped.item": "{name}: {reason}",
  "job.manual.title": "Import it by hand",
  "job.manual.intro":
    "Or do it yourself in Radarr or Sonarr: open Manual Import there and choose the folder shown here.",
  "job.manual.folder": "Folder in {app}",
  "job.manual.copy": "Copy",
  "job.manual.copyFor": "Copy the {app} folder",
  "job.manual.copied": "Copied",
  "job.manual.copyFailed": "Could not copy. Select the folder text and copy it.",
  "job.manual.open": "Open {app}",
  "job.actions": "What you can do",
  "job.action.cancel": "Cancel upload",
  "job.action.retry": "Try again",
  "job.action.delete": "Delete",
  "job.confirm.back": "Keep it as it is",
  "job.confirm.upload": "Upload",
  "job.confirm.files": "Files",
  "job.confirm.size": "Size",
  "job.confirm.cancel.title": "Cancel this upload?",
  "job.confirm.cancel.warning":
    "The files of this upload are deleted from the server. This cannot be undone.",
  "job.confirm.cancel.importWarning":
    "The import may still be running in the app: it can still finish and put the files in the library. Check the title in the app afterwards.",
  "job.confirm.retry.title": "Try this upload again?",
  "job.confirm.delete.title": "Delete this upload?",
  "job.confirm.delete.warning": "The upload and its files are deleted for good.",

  "state.uploading": "Uploading",
  "state.uploaded": "Uploaded",
  "state.probing": "Checking",
  "state.probed": "Checked",
  "state.matched": "Matched",
  "state.awaiting-confirmation": "Waiting for you",
  "state.importing": "Importing",
  "state.imported": "Imported",
  "state.post-processing": "Finishing",
  "state.done": "Done",
  "state.failed": "Failed",
  "state.stalled": "Stalled",
  "state.cancelled": "Cancelled",

  "stateHint.uploading": "Files are still being sent.",
  "stateHint.uploaded": "All files arrived. The checks start next.",
  "stateHint.probing": "Checking what is inside the files.",
  "stateHint.probed": "The files are checked and ready.",
  "stateHint.matched": "The film or show was found.",
  "stateHint.awaiting-confirmation": "Waiting for you to confirm.",
  "stateHint.importing": "Being added to the library.",
  "stateHint.imported": "Added to the library.",
  "stateHint.post-processing": "Finishing: subtitles and the media server.",
  "stateHint.done": "Finished.",
  "stateHint.failed": "It stopped and needs you.",
  "stateHint.stalled": "Waiting for something outside Fukku. It continues by itself.",
  "stateHint.cancelled": "Cancelled. The files were deleted.",

  "jobError.worker_unavailable":
    "The part of Fukku that checks files is not answering. The upload continues by itself when it is back.",
  "jobError.free_space":
    "There is not enough free space on the server to unpack a zip. The upload continues by itself when there is room. If it takes long, tell the person who runs the server.",
  "jobError.task_timeout":
    "Checking took too long and was stopped. Try again; if it fails again, the file may be damaged.",
  "jobError.task_failed":
    "Checking the files failed. Try again; if it fails again, tell the person who runs the server.",
  "jobError.no_usable_files":
    "None of the files could be used. The list below says why each one was refused.",
  "jobError.unknown":
    "Something went wrong with this upload (code: {code}). Try again, or tell the person who runs the server.",

  "refusal.not_a_zip": "This is not a valid zip file, or it is damaged.",
  "refusal.too_many_entries": "The zip has too many files inside.",
  "refusal.too_large": "Unpacked, the zip would be too large.",
  "refusal.ratio": "The zip is compressed in a suspicious way and was not opened.",
  "refusal.free_space": "There was not enough room to unpack it.",
  "refusal.extension": "This kind of file is not accepted: only video, subtitles and zip.",
  "refusal.no_video_stream": "No video was found inside this file. It may be damaged or renamed.",
  "refusal.no_duration": "The file has no readable length. It may be damaged or incomplete.",
  "refusal.not_text": "This subtitle file is not readable text.",
  "refusal.unreadable": "The file could not be read. It may be damaged.",
  "refusal.timeout": "Checking this file took too long.",
  "refusal.too_many_streams":
    "This file has more audio, video and subtitle tracks than the server accepts.",
  "refusal.result_too_large":
    "The report for this upload's files grew too large to hold this file. Upload it in a separate job.",
  "refusal.outside_library": "The file is outside the folders Fukku may read.",
  "refusal.unknown": "The file was refused (code: {code}).",

  "skip.traversal": "its name points outside the folder",
  "skip.absolute": "its name is an absolute path",
  "skip.drive_letter": "its name starts with a drive letter",
  "skip.backslash": "its name contains a backslash",
  "skip.symlink": "it is a link, not a file",
  "skip.special": "it is a special system file",
  "skip.nested_archive": "it is a zip inside the zip",
  "skip.extension": "this kind of file is not accepted",
  "skip.name_too_long": "its name is too long",
  "skip.duplicate": "another file already has this name",
  "skip.encrypted": "it is password-protected",
  "skip.hidden": "its name starts with a dot",
  "skip.unknown": "skipped (code: {code})",

  "errors.unknown": "Something went wrong.",
  "errors.signed_out": "Your session ended. Sign in again.",
  "errors.network": "The server cannot be reached. Check your connection.",
  "errors.bad_response": "The server sent an answer the page cannot read.",
  "errors.forbidden": "You are not allowed to do that.",
  "errors.origin_mismatch": "The request came from another site and was refused.",
  "errors.not_found": "Not found.",
  "errors.bad_request": "The request was not valid.",
  "errors.too_large": "The request is too large.",
  "errors.internal": "The server had a problem. Try again later.",
  "errors.uploads_unfinished": "Some uploads are not finished yet.",
  "errors.no_files": "Add at least one file first.",
  "errors.wrong_state": "That is not possible while the upload is in this state.",
  "errors.job_active": "This upload is still in progress.",
  "errors.bad_name": "The file name is not valid.",
  "errors.job_not_uploading": "This upload no longer takes files.",
  "errors.duplicate_name": "A file with this name was already added.",
  "errors.file_too_large": "The file is larger than the server allows.",
  "errors.extension_not_allowed": "This type of file is not accepted.",
  "errors.too_many_uploads": "Too many uploads at once. Wait for one to finish.",
  "errors.too_many_jobs": "You have too many unfinished uploads. Finish or cancel one first.",
  "errors.in_flight_limit": "The server is receiving as much as it can. Try again later.",
  "errors.insufficient_space": "The server does not have enough free space.",
  "errors.storage_unavailable": "Storage is not available right now. Try again later.",
  "errors.offset_mismatch":
    "The upload got out of step with the server. It continues from where the server is.",
  "errors.upload_busy": "The server is still working on this upload. Try again in a moment.",

  "job.match": "Choose the film or show",
  "job.match.body":
    "Say which title these files are, then check them and import. Radarr or Sonarr does the import.",
  "job.target.title": "Chosen title",
  "job.target.body": "{title} ({year}) in {app}",
  "job.target.change": "Choose a different title",
  "match.title": "Choose the film or show",
  "match.intro":
    "Say which title these files are. Fukku adds it to Radarr or Sonarr if it is new; nothing is downloaded.",
  "match.back": "Back to the upload",
  "match.manual": "Import it by hand instead",
  "match.kind.legend": "What is it?",
  "match.kind.movie": "A film",
  "match.kind.series": "A show",
  "match.kind.hint": "Chosen from the files you uploaded. Change it if that is wrong.",
  "match.search.label": "Title",
  "match.search.hint":
    "Nothing found? Try the English title, or an id: tmdb:<id> for a film, tvdb:<id> for a show.",
  "match.search.submit": "Search",
  "match.results.title": "Results",
  "match.results.count.one": "{count} result",
  "match.results.count.other": "{count} results",
  "match.results.none.title": "Nothing found for “{term}”",
  "match.results.none.body":
    "Try the English title, a shorter name, or an id such as tmdb:<id> or tvdb:<id>.",
  "match.hit.name": "{title} ({year})",
  "match.hit.inLibrary": "Already in {app}",
  "match.hit.hasFile": "Already has a file",
  "match.hit.seasons.one": "{count} season",
  "match.hit.seasons.other": "{count} seasons",
  "match.hit.noOverview": "No description.",
  "match.options.title": "Options",
  "match.options.inLibrary": "{title} is already in {app}. Its settings stay as they are.",
  "match.profile": "Quality profile",
  "match.profile.choose": "Choose a profile",
  "match.profile.needed": "Choose a quality profile to continue.",
  "match.folder": "Root folder",
  "match.monitored": "Replace it when a better copy appears",
  "match.seriesType": "Type of show",
  "match.seriesType.standard": "Standard",
  "match.seriesType.anime": "Anime",
  "match.seriesType.daily": "Daily",
  "match.submit": "Use this title",
  "match.noApps.title": "Radarr and Sonarr are not set up",
  "match.noApps.body":
    "Ask whoever runs Fukku to connect one of them. Meanwhile you can import the files by hand.",
  "match.wrongState.title": "This upload cannot be matched now",
  "match.wrongState.body": "A title can only be chosen once the files are checked.",
  "errors.title_in_use": "Another upload is already working on this title. Wait for it to finish.",
  "errors.title_busy": "This title is downloading right now. Try again when it has finished.",
  "errors.arr_unavailable": "Radarr or Sonarr cannot be reached right now. Try again in a moment.",
  "match.profile.none": "{app} has no quality profiles. Add one there, then come back.",
  "match.unavailable.title": "Radarr and Sonarr are not answering",
  "errors.app_not_configured": "That app is not set up in Fukku.",
  "errors.title_not_found": "The app does not know that title any more. Search again.",
  "job.mapping": "Check the files",
  "mapping.title": "Check the files",
  "mapping.back": "Back to the upload",
  "mapping.target": "{title} ({year}) in {app}",
  "mapping.intro":
    "Each file has its own row, with what {app} makes of it. Change what is wrong: {app} checks every change again.",
  "mapping.fromNames":
    "The season and the episodes come from the file names, or from {app}'s best guess. Check them against what each file really is.",
  "mapping.pending.title": "{app} is looking at the files",
  "mapping.pending.body": "This takes a moment. The rows appear here by themselves.",
  "mapping.wrongState.title": "The files cannot be checked now",
  "mapping.wrongState.body":
    "This upload is not waiting for its files to be checked. Open the upload to see where it is.",
  "mapping.openJob": "Open the upload",
  "mapping.summary.label": "Summary",
  "mapping.summary.toImport.one": "{count} to import",
  "mapping.summary.toImport.other": "{count} to import",
  "mapping.summary.skipped.one": "{count} skipped",
  "mapping.summary.skipped.other": "{count} skipped",
  "mapping.summary.attention.one": "{count} needs attention",
  "mapping.summary.attention.other": "{count} need attention",
  "mapping.filter": "Only files that need attention",
  "mapping.filter.empty": "No file needs attention.",
  "mapping.table.label": "Files and where they will go",
  "mapping.col.select": "Select",
  "mapping.col.file": "File",
  "mapping.col.season": "Season",
  "mapping.col.episodes": "Episodes",
  "mapping.col.quality": "Quality",
  "mapping.col.languages": "Languages",
  "mapping.col.notes": "Check",
  "mapping.col.skip": "Skip",
  "mapping.select.row": "Select {name}",
  "mapping.select.all": "Select all the files shown",
  "mapping.duration": "{actual}, expected {expected}",
  "mapping.duration.only": "{actual}",
  "mapping.season.for": "Season of {name}",
  "mapping.season.option": "Season {season}",
  "mapping.season.unknown": "Not set",
  "mapping.episode.first": "Episode of {name}",
  "mapping.episode.second": "Second episode of {name}",
  "mapping.episode.choose": "Choose an episode",
  "mapping.episode.none": "No second episode",
  "mapping.episode.option": "{number}. {title}",
  "mapping.episode.hasFile": "{number}. {title} (already has a file)",
  "mapping.quality.for": "Quality of {name}",
  "mapping.quality.none": "Not offered",
  "mapping.skip.for": "Skip {name}",
  "mapping.skip.label": "Skip",
  "mapping.skip.server": "Skipped by the server",
  "mapping.skip.server.hint":
    "The server skips this row because its episode already has a file. Tick the box to skip it yourself.",
  "mapping.checking": "Checking…",
  "mapping.failed": "The change was not checked.",
  "mapping.failed.wrongState":
    "The files changed while you were editing, so the list was reloaded. Try your change again.",
  "mapping.retry": "Try again",
  "mapping.discard": "Undo my change",
  "mapping.ok": "No problems found.",
  "mapping.state.attention": "Needs attention",
  "mapping.state.refused": "Refused",
  "mapping.state.skipped": "Skipped",
  "mapping.state.failed": "Not checked",
  "mapping.languages.none": "None found",
  "mapping.notOffered":
    "{app} did not offer this file, so it cannot be changed or imported. You can only skip it.",
  "mapping.rejection.permanent":
    "{app} refuses this file: “{reason}”. To import it anyway, confirm it on the next step, or skip it here.",
  "mapping.rejection.temporary":
    "{app} refuses this file for now: “{reason}”. That can change by itself. To import it anyway, confirm it on the next step.",
  "mapping.warning.double_episode":
    "This file is about twice as long as an episode. It may hold two.",
  "mapping.warning.double_episode.action": "Make it episodes {first} and {second}",
  "mapping.warning.runtime_mismatch":
    "Its length is far from the episode's. The numbering, or the episode order, may be wrong.",
  "mapping.warning.duplicate_episode": "Another file is set to the same episode.",
  "mapping.warning.existing_file":
    "This episode already has a file in {app}. It cannot be imported yet, so the file is skipped.",
  "mapping.warning.duplicate_film":
    "Another file of this film is not skipped either. A film has one file: skip the one you do not want.",
  "mapping.warning.not_offered": "{app} did not offer this file.",
  "mapping.warning.language":
    "The audio may not be in the original language. Languages: {languages}. Score: {score}; the quality profile asks for at least {min}.",
  "mapping.warning.language.formats": "Rules that matched: {formats}.",
  "mapping.warning.unknown": "Something to check ({code}).",
  "mapping.hint.dvd_order.title": "The episode order may be different",
  "mapping.hint.dvd_order.body":
    "Most files are much longer or shorter than their episodes. The show may use another order (DVD order). Check the numbering.",
  "mapping.hint.unknown": "A hint from the app ({code}).",
  "mapping.bulk.label": "Change the selected files",
  "mapping.bulk.selected.one": "{count} file selected",
  "mapping.bulk.selected.other": "{count} files selected",
  "mapping.bulk.none": "Select files to move their episodes or set their season together.",
  "mapping.bulk.clear": "Clear selection",
  "mapping.bulk.shift": "Move episodes by",
  "mapping.bulk.shift.apply": "Move episodes",
  "mapping.bulk.season": "Season",
  "mapping.bulk.season.apply": "Set the season",
  "mapping.bulk.checking": "Checking the selected files…",
  "mapping.bulk.failed": "The change to the selected files was not checked.",
  "mapping.continue": "Continue to the confirmation",
  "mapping.continue.attention":
    "Files that need attention are confirmed one by one on the next step.",
  "mapping.continue.none": "Nothing to import. Fix a file or stop skipping it first.",
  "mapping.continue.busy": "Wait for the checks to finish.",

  // ---- confirmation, progress and result (#44) ------------------------------------------
  "job.review": "Review and import",
  "jobError.import_failed":
    "The app could not import every file. The list below shows which ones arrived. Check the app, then try again.",
  "jobError.arr_unavailable":
    "Waiting for {app}, which is not answering. There is nothing for you to do: the import continues by itself when it is back.",
  "jobError.proposal_failed":
    "The app refused the files or could not check them again. Nothing was imported. Try again; if it fails again, tell the person who runs the server.",
  "jobError.title_busy":
    "The app is downloading this title right now, so the files could not be imported. Try again when the download has finished.",
  "jobError.library_has_file":
    "The title got a file in the library after the confirmation, and this import would replace it, so nothing was sent. Upload the files again to choose what to do with the file the library has now.",
  "errors.needs_override":
    "Some files have problems you have not agreed to yet. Tick “Import anyway” for each, or leave them out.",
  "errors.admin_required":
    "Only an admin can make this change to the library on this server. Ask an admin to confirm this upload, or leave out what would change the library.",
  "errors.incomplete_rows":
    "Some files have no film or episode to go to. Choose one for each, or leave them out.",
  "errors.flow2":
    "This film now has a file in Radarr. Match it again so Fukku can offer to merge the new language or keep it as a second version.",
  "errors.nothing_to_import": "There is nothing to import: every file is left out.",

  "confirm.title": "Confirm the import",
  "confirm.back": "Back to the upload",
  "confirm.error.title": "The import could not be prepared",
  "confirm.wrongState.title": "This upload cannot be confirmed now",
  "confirm.wrongState.body": "It can only be confirmed after a film or show is chosen.",
  "confirm.wrongState.action": "Go to the upload",
  "confirm.pending": "{app} is looking at the files. This takes a moment.",
  "confirm.intro":
    "{app} has looked at each file. Check what it found, correct what is wrong, then import.",
  "confirm.target": "Goes into {app} as",
  "confirm.title.name": "{title} ({year})",
  "confirm.rows.title.one": "{count} file",
  "confirm.rows.title.other": "{count} files",
  "confirm.row.goesTo": "Goes to ({app})",
  "confirm.row.episode": "S{season}E{number} {title}",
  "confirm.row.file": "Your file",
  "confirm.row.noDuration": "Length unknown",
  "confirm.quality": "Quality",
  "confirm.languages": "Languages",
  "confirm.languages.none": "None found",
  "confirm.languages.remove": "Remove {name}",
  "confirm.languages.add": "Add a language",
  "confirm.languages.choose": "Add a language…",
  "confirm.formats": "Custom formats",
  "confirm.formats.none": "None",
  "confirm.score": "Custom format score",
  "confirm.checking": "{app} is checking your change…",
  "confirm.issues.title": "Read this before you import",
  "confirm.rejection": "{app} refuses this file:",
  "confirm.override": "Import anyway",
  "confirm.state.ready": "Ready to import",
  "confirm.state.issues": "Needs your decision",
  "confirm.state.agreed": "Imported anyway",
  "confirm.state.out": "Left out",
  "confirm.count.label": "What will happen",
  "confirm.count.in.one": "{count} file will be imported",
  "confirm.count.in.other": "{count} files will be imported",
  "confirm.count.out.one": "{count} is left out",
  "confirm.count.out.other": "{count} are left out",
  "confirm.override.missing": "Tick “Import anyway” for each file with a problem, or leave it out.",
  "confirm.override.adminOnly":
    "{app} refused this file, or it would replace a file in the library, so only an admin can import it anyway. Leave it out, or ask an admin to confirm this upload.",
  "confirm.override.adminMissing":
    "Only an admin can import files that {app} refused or that would replace a file in the library. Leave them out to import the rest, or ask an admin to confirm this upload.",
  "confirm.admin.leaveOut.hint.one":
    "You can leave this file out and import the rest: leave it out here, then press Import again.",
  "confirm.admin.leaveOut.hint.other":
    "You can leave these files out and import the rest: leave them out here, then press Import again.",
  "confirm.override.stale":
    "The server found problems this page did not show. They are shown now: read them, then try again.",
  "confirm.skip": "Leave this file out",
  "confirm.skip.undo": "Include this file again",
  "confirm.skipped": "This file is left out and will not be imported.",
  "confirm.nothing": "Every file is left out, so there is nothing to import.",
  "confirm.submit": "Import",
  "confirm.change": "Choose a different title",
  "confirm.warning.not_offered": "The app did not recognise this file, so it cannot import it.",
  "confirm.warning.existing_file":
    "An episode in this file already has a file in the library. Replacing files comes in a later version.",
  "confirm.warning.double_episode":
    "This file is about twice as long as one episode. It may hold two episodes: check which ones it covers.",
  "confirm.warning.runtime_mismatch":
    "This file is much shorter or longer than the episode it is matched to. It may be the wrong episode or a different cut.",
  "confirm.warning.duplicate_episode":
    "Another file is matched to the same episode. Only one of them can be imported.",
  "confirm.warning.duplicate_film":
    "More than one file is set to be imported for this film, and a film has one file. Leave the others out.",
  "confirm.warning.language":
    "The languages found in this file may not be the ones you want for this title, for example a dub where the original is wanted. Check them before you import.",
  "confirm.warning.language.facts":
    "Custom formats: {formats}. Score {score}; the profile asks for at least {min}.",
  "confirm.warning.unknown": "The app or Fukku noticed something about this file (code: {code}).",
  "confirm.hint.dvd_order":
    "Most episodes are far from the expected length. The files may follow the DVD order instead of the air order: check the episode numbers.",
  "confirm.hint.unknown": "A note from the server (code: {code}).",
  "confirm.subtitles.title": "Subtitle files",
  "confirm.subtitles.intro":
    "These files are handed to the subtitle manager after the import. The language and flags are guessed from the file name: correct them if they are wrong.",
  "confirm.subtitles.language": "Language of {name} (two letters, such as en or es)",
  "confirm.subtitles.noLanguage": "Type the language as two letters, such as en or es.",
  "confirm.subtitles.invalid":
    "Give every subtitle file a language of two letters, such as en or es.",
  "confirm.subtitles.guessHint": "Guessed from the file name.",
  "confirm.subtitles.forced": "{name} is forced",
  "confirm.subtitles.forced.short": "Forced",
  "confirm.subtitles.hi": "{name} is for the hard of hearing",
  "confirm.subtitles.hi.short": "For the hard of hearing",

  "import.title": "Importing",
  "import.title.done": "Result",
  "import.app": "the app",
  "import.running": "{app} is moving the files into the library. This page updates by itself.",
  "import.waiting": "The import waits for {app} and continues by itself.",
  "import.count": "{done} of {total} files in the library",
  "import.rows": "Files",
  "import.row": "File {n} of {total}",
  "import.row.waiting": "Waiting",
  "import.row.imported": "Imported",
  "import.row.skipped": "Left out",
  "import.row.failed": "Not imported",
  "import.steps": "Steps after the import",
  "import.post.running": "The files are in the library. These steps finish the job.",
  "import.step.mediaServerRefresh": "Tell the media server about the new files",
  "import.step.mediaServerVersions": "Group the second version with the film in the media server",
  "import.step.subtitles": "Hand the subtitles to the subtitle manager",
  "import.step.mediaServerRefresh.failed":
    "The media server did not show the title in time. It will show up after its next library scan, or start a scan there by hand.",
  "import.step.mediaServerVersions.failed":
    "The media server did not group the version with the film. Fukku tries again by itself; until then the two show as separate entries.",
  "import.step.subtitles.failed":
    "The subtitles were not written. Add them in the subtitle manager by hand. The files stay in this upload until it expires.",
  "import.step.failed":
    "This step failed. The import itself is not undone. Tell the person who runs the server.",
  "import.status.pending": "Working",
  "import.status.ok": "Done",
  "import.status.failed": "Failed",
  "import.status.skipped": "Not used",
  "import.done.title": "{title} is in the library",
  "import.done.body": "{app} imported the files.",
  "import.done.failedSteps": "One of the steps after it failed: see below.",
  "import.link": "Open the title in the media server",
  "import.link.none": "There is no link to the media server for this title.",
  "import.unmonitored.title": "This title is not monitored",
  "import.unmonitored.chosen":
    "You chose not to replace this file when a better copy appears, so automatic upgrades will not replace it. Pressing the title's own Search button in {app} still can.",
  "import.unmonitored.body":
    "Only a dub was imported, so the title is now unmonitored: automatic upgrades will not replace this file. Pressing the title's own Search button in {app} still can, so do not use it if you want to keep this one.",

  // ---- Flow 2: another language of a film already in the library (#59) ----
  "match.exists.title": "This film already has a file",
  "match.exists.body":
    "If your upload is another language of this film, Fukku compares it with the file in the library. Then you can merge the new audio into that file, or keep your upload as a second version.",
  "match.exists.action": "Add another language",
  "errors.already_in_library":
    "This film already has a file. Choose it again to add another language of it.",
  "job.flow2": "Compare with the library file",
  "job.flow2.body":
    "This film already has a file. Compare your upload with it, then merge the new audio or keep it as a second version.",
  "flow2.back": "Back to the upload",
  "flow2.title": "Another language of this film",
  "flow2.subject": "{title} ({year}) in Radarr",
  "flow2.intro":
    "This film already has a file in Radarr. Fukku compares your upload with it, then you choose what to do.",
  "flow2.error.title": "The comparison could not be shown",
  "flow2.wrongState.title": "This upload cannot be compared now",
  "flow2.wrongState.body":
    "It can be compared once a film that already has a file is chosen, and until you confirm.",
  "flow2.wrongState.action": "Go to the upload",
  "flow2.probed.title": "Choose the film again",
  "flow2.probed.body":
    "No film is chosen for this upload, or the film’s file in Radarr changed since you chose it. Choose the film again and the comparison is made again.",
  "flow2.notFlow2.title": "This film has no file in the library",
  "flow2.notFlow2.body":
    "There is nothing to compare. Review the files and import them as a new film.",
  "flow2.pending": "Radarr’s file is being read. This takes a moment.",
  "flow2.noUpload.title": "There is no video to compare",
  "flow2.noUpload.body": "No video of this upload could be read. Go back and check its files.",
  "flow2.changed.action": "Choose the film again",
  "flow2.compare.title": "Your upload and the library file",
  "flow2.side.library": "In the library",
  "flow2.side.upload": "Your upload",
  "flow2.file": "File",
  "flow2.size": "Size",
  "flow2.quality": "Quality",
  "flow2.languages": "Languages",
  "flow2.duration": "Length",
  "flow2.audio": "Audio",
  "flow2.subtitles": "Subtitles",
  "flow2.unknown": "Not known",
  "flow2.seconds": "{value}\u00a0s",
  "flow2.finding.match.title": "Same cut",
  "flow2.finding.match.body":
    "The two files are the same length, within {delta}. The audio of your upload can be added to the library file.",
  "flow2.finding.pal.title": "A PAL speed-up",
  "flow2.finding.pal.body":
    "The two files differ by {delta}, a ratio of {ratio}: the one of a film sped up from 23.976 to 25 frames a second. Its audio would drift against the library file’s picture, so it cannot be merged. It can be kept as a second version.",
  "flow2.finding.different.title": "A different cut",
  "flow2.finding.different.body":
    "The two files differ by {delta} (a ratio of {ratio}), and not the way a speed-up does. It may be a longer or shorter edit of the film. Its audio cannot be merged, but it can be kept next to the library file as a second version.",
  "flow2.options.title": "What you can do",
  "flow2.offer.merge": "Merge the audio into the library file",
  "flow2.offer.merge.body":
    "The new audio becomes an extra track of the file in the library. One file stays, and Radarr replaces its file with the merged one.",
  "flow2.offer.version": "Keep it as a second version",
  "flow2.offer.version.body":
    "Your upload is kept next to the film as another version, and the media server lists it with the film. The file in the library is not touched.",
  "flow2.offer.legend": "How do you want to add it?",
  "flow2.notOffered.title": "Not offered",
  "flow2.notOffered.item": "{offer}: {reason}",
  "flow2.reason.merge_disabled": "merging is switched off on this server.",
  "flow2.reason.versions_disabled": "second versions are switched off on this server.",
  "flow2.reason.versions_unsupported":
    "second versions need a media server that can group them (Jellyfin), and none is set up.",
  "flow2.reason.runtime_differs": "merging needs the same cut, and this is not.",
  "flow2.reason.unknown": "not possible here (code: {code}).",
  "flow2.reason.admin_required":
    "it changes a file the library already has, so only an admin can confirm it. An admin can open this upload and confirm it.",
  "flow2.admin.title": "An admin must confirm this upload",
  "flow2.admin.body":
    "Merging into the film’s file and keeping a second version both change the library, which only an admin can do on this server. Nothing was changed.",
  "flow2.admin.body.merge":
    "Merging into the film’s file changes the library, which only an admin can do on this server. Nothing was changed.",
  "flow2.admin.body.version":
    "Keeping a second version changes the library, which only an admin can do on this server. Nothing was changed.",
  "flow2.nothing.title": "Nothing can be done with this upload here",
  "flow2.nothing.body":
    "Ask whoever runs Fukku to change this, or import the files by hand. Nothing was changed.",
  "flow2.sync.title": "Check that the audio is in sync",
  "flow2.sync.intro":
    "The same length does not prove the audio lines up. Fukku makes three short clips with the film’s picture and the new audio: from the start, the middle and the end. In each one, watch a moment where you can see a sound being made, such as someone speaking or a door closing.",
  "flow2.sync.audio": "Audio track to add",
  "flow2.sync.track": "{track} ({detail})",
  "flow2.sync.offset": "Offset in milliseconds",
  "flow2.sync.offset.hint":
    "Leave it at 0 for the first clips. You can also type a number: negative moves the new audio earlier, positive later. From −60000 to 60000.",
  "flow2.sync.offset.unit": "ms",
  "flow2.sync.offset.late": "The sound comes after the picture",
  "flow2.sync.offset.early": "The sound comes before the picture",
  "flow2.sync.offset.earlier": "{ms} ms earlier",
  "flow2.sync.offset.later": "{ms} ms later",
  "flow2.sync.offset.say.none": "The new audio is not moved.",
  "flow2.sync.offset.say.later": "The new audio starts {seconds} later.",
  "flow2.sync.offset.say.earlier": "The new audio starts {seconds} earlier.",
  "flow2.sync.offset.invalid": "Enter a whole number from −60000 to 60000.",
  "flow2.sync.make": "Make the clips",
  "flow2.sync.again": "Make the clips again",
  "flow2.sync.pending": "Making the clips… This takes a moment, and the page updates by itself.",
  "flow2.sync.failed.title": "The clips could not be made",
  "flow2.sync.error.outside_library": "The library file is outside the folders Fukku may read.",
  "flow2.sync.error.library_unreadable": "The library file could not be read.",
  "flow2.sync.error.upload_unreadable": "The uploaded file could not be read.",
  "flow2.sync.error.render_failed": "The clips could not be made from these files.",
  "flow2.sync.error.timeout": "Making a clip took too long.",
  "flow2.sync.error.too_large": "A clip came out too large.",
  "flow2.sync.error.task_failed": "The worker could not make the clips.",
  "flow2.sync.error.task_timeout": "The worker took too long to make the clips.",
  "flow2.sync.error.unknown": "The clips could not be made (code: {code}).",
  "flow2.sync.ready": "Made with {track} at an offset of {offset}\u00a0ms.",
  "flow2.sync.clips": "Clips",
  "flow2.sync.clip": "Clip {n} of {total}, from {at}",
  "flow2.sync.clip.fallback": "Your browser cannot play this clip.",
  "flow2.sync.stale":
    "The track or the offset changed after these clips were made. Make the clips again to check it.",
  "flow2.sync.match": "They match, continue",
  "flow2.sync.nomatch":
    "If the sound is early or late by the same amount in every clip, change the offset above and make the clips again.",
  "flow2.sync.drift":
    "If it lines up at the start and not at the end, the two files run at different speeds. An offset cannot fix that: keep the upload as a second version instead.",
  "flow2.sync.question": "Do the sound and the picture line up in every clip?",
  "flow2.sync.confirmed": "Checked at an offset of {offset}\u00a0ms.",
  "flow2.refusal.title": "The last merge was refused",
  "flow2.refusal.choice":
    "It used {track} at an offset of {offset}\u00a0ms. Nothing in the library changed.",
  "flow2.refusal.hint":
    "Choose another offset or audio track, make the clips again, check them and confirm the merge again.",
  "flow2.merge.title": "Merge into the library file",
  "flow2.merge.intro":
    "Choose what is added. Everything below is optional: what you leave empty uses the server’s settings.",
  "flow2.merge.subtitles": "Subtitles to add",
  "flow2.merge.subtitles.hint": "They get the same offset as the audio.",
  "flow2.merge.subtitles.none": "The upload has no subtitles inside it.",
  "flow2.merge.tag": "Language tag",
  "flow2.merge.tag.hint": "A tag such as es-419. Empty: the server’s default.",
  "flow2.merge.tag.invalid":
    "That is not a valid language tag. Use letters and hyphens, such as es-419 or en-US, in lower case for the language.",
  "flow2.merge.name": "Track name",
  "flow2.merge.name.hint": "How the track is named in players. Empty: the server’s default.",
  "flow2.merge.keepDefault": "Keep the original audio as the default track",
  "flow2.merge.confirm.title": "Merge the audio into the library file?",
  "flow2.merge.fact.audio": "Audio added",
  "flow2.merge.fact.offset": "Offset",
  "flow2.merge.fact.subtitles": "Subtitles added",
  "flow2.merge.fact.subtitles.none": "None",
  "flow2.merge.fact.tag": "Language tag",
  "flow2.merge.fact.name": "Track name",
  "flow2.merge.fact.default": "Server default",
  "flow2.merge.fact.file": "The file in the library",
  "flow2.merge.fact.file.value":
    "Radarr replaces it with the merged file, which keeps all its tracks and has the new ones too.",
  "flow2.merge.fact.title": "The film",
  "flow2.merge.fact.title.value":
    "It is left unmonitored afterwards, unless the server is set otherwise, so automatic upgrades do not replace the merged file.",
  "flow2.merge.warning.replace":
    "The old file is removed from the library. If Radarr has no recycle bin set up, it cannot be brought back.",
  "flow2.merge.warning.search":
    "Pressing the film’s own Search button in Radarr can still replace the merged file with a release that has no new audio.",
  "flow2.merge.submit": "Merge into the library file",
  "flow2.version.title": "Keep as a second version",
  "flow2.version.intro":
    "Your upload is kept next to the film, outside Radarr’s folders, and grouped with the film in the media server.",
  "flow2.version.label": "Version label",
  "flow2.version.label.hint":
    "Shown with the film in the media server, for example Español. Do not use quality words such as 1080p or WEB-DL.",
  "flow2.version.name": "File name: {name}",
  "flow2.version.problem.title": "This label cannot be used as it is",
  "flow2.version.problem.label_empty":
    "No label could be proposed, because the audio of your upload has no language tag. Type one.",
  "flow2.version.problem.label_invalid":
    'The label cannot be part of a file name. Remove slashes and characters such as : * ? " < > |.',
  "flow2.version.problem.label_too_long": "The label is too long. Use a shorter one.",
  "flow2.version.problem.label_forbidden":
    "“{token}” is a word kept out of labels (quality and source words). Remove it.",
  "flow2.version.problem.label_extra":
    "The media server takes a file that ends in this word for an extra, such as a trailer, and not for a version. Change the last word.",
  "flow2.version.problem.label_quality":
    "Radarr reads “{token}” in the name as a quality. Remove it, or Radarr could take the file for a better copy.",
  "flow2.version.problem.label_unchecked":
    "Radarr could not read the name, so the label could not be checked. Try a simpler label.",
  "flow2.version.problem.unknown": "The label has a problem (code: {code}).",
  "flow2.version.problem.admin":
    "You run this server: the words kept out of labels are the setting versions.forbiddenLabelTokens, and the proposed label comes from versions.labelTemplate.",
  "flow2.version.notices": "Good to know",
  "flow2.version.notice.outside_radarr":
    "Radarr does not know this version. It never renames, upgrades or deletes it, and only replaces the film’s own file.",
  "flow2.version.notice.regrouped_after_changes":
    "When Radarr renames or upgrades the film’s file, the media server loses the grouping. Fukku groups them again by itself, and until then the two show as separate entries.",
  "flow2.version.notice.kept_on_delete":
    "If you delete the film in Radarr, the version stays on disk. Remove it by hand.",
  "flow2.version.notice.subtitles_not_handed_over":
    "The subtitle files of this upload are not handed to the subtitle manager, which only knows the film’s own file.",
  "flow2.version.notice.unknown": "A note from the server (code: {code}).",
  "flow2.version.confirm.title": "Keep this as a second version?",
  "flow2.version.fact.label": "Label",
  "flow2.version.fact.name": "File name",
  "flow2.version.fact.where": "Where it goes",
  "flow2.version.fact.where.value":
    "In the folder the server keeps for versions, not in Radarr’s. The library file is not touched.",
  "flow2.version.fact.server": "In the media server",
  "flow2.version.fact.server.value": "Listed with the film, as another version to choose.",
  "flow2.version.submit": "Keep as a second version",
  "errors.not_flow2": "This upload is not another language of a film that is in the library.",
  "errors.merge_not_offered":
    "This upload cannot be merged into the library file. The comparison says why.",
  "errors.no_audio": "The upload has no audio track to add.",
  "errors.preview_required":
    "The sync clips are out of date. Make them again at the offset you want, check them, then merge.",
  "errors.library_changed":
    "The film’s file in Radarr changed since you chose the film. Choose the film again so Fukku compares the new file.",
  "errors.version_label": "That label cannot be used. Change it and try again.",
  "errors.version_unavailable":
    "A second version is not possible right now: it is switched off, a folder cannot be used, or some files cannot be versions. Ask whoever runs Fukku.",
  "errors.version_exists": "A version with this name already exists. Choose another label.",
  "errors.season_dub_choice": "Some episodes already have a file. Choose what to do with them.",
  "errors.shared_file":
    "Replacing some of these files would also remove the file of episodes that are not in this upload.",
  "jobError.library_unreadable":
    "Fukku could not read the film’s file in the library. Tell the person who runs the server.",
  "jobError.merge_failed":
    "The merge could not be made. The library was not changed. You can try again.",
  "jobError.merge_rejected":
    "Radarr did not take the merged file. The library was not changed. Tell the person who runs the server.",
  "jobError.version_failed":
    "The second version could not be placed. The library was not changed. Tell the person who runs the server.",
  "import.merge.title": "The merge",
  "import.merge.steps": "Steps of the merge",
  "import.merge.step.make": "Make the merged file",
  "import.merge.step.import": "Radarr replaces its file",
  "import.merge.pending": "The merge is waiting its turn.",
  "import.merge.running":
    "The audio is being merged into a copy of the library file. For a large film this takes a few minutes. This page updates by itself.",
  "import.merge.merged": "The merged file is ready: {name} ({size}).",
  "import.merge.failed": "The merge did not finish. The library file was not changed.",
  "import.merge.refusal.library_unreadable": "The library file could not be read.",
  "import.merge.refusal.upload_unreadable": "The uploaded file could not be read.",
  "import.merge.refusal.track_not_found": "A chosen track is not in the upload.",
  "import.merge.refusal.free_space": "There was not enough free space for the merged file.",
  "import.merge.refusal.mux_failed": "The tool that merges the tracks reported a problem.",
  "import.merge.refusal.library_changed": "The library file changed while it was being read.",
  "import.merge.refusal.verify_failed":
    "The merged file did not pass the checks (the picture, the length or the tracks were not as expected).",
  "import.merge.refusal.busy": "Another merge was using the same place. Try again.",
  "import.merge.refusal.unknown": "The merge was refused (code: {code}).",
  "import.merge.fact.audio": "Audio tracks",
  "import.merge.fact.subtitles": "Subtitle tracks",
  "import.merge.fact.offset": "Offset",
  "import.merge.fact.tag": "Language tag",
  "import.merge.fact.name": "Track name",
  "import.merge.fact.original": "Original audio stays the default track",
  "import.merge.fact.yes": "Yes",
  "import.merge.fact.no": "No",
  "import.merge.fact.offset.value": "{offset}\u00a0ms",
  "import.merge.done.title": "The merged file is in the library",
  "import.merge.done.body": "{app} replaced its file with {name}.",
  "import.merge.warning.search_can_replace.title": "This film is not monitored",
  "import.merge.warning.search_can_replace":
    "Automatic upgrades and the Wanted searches leave the merged file alone. But pressing the film’s own Search button in {app} can still replace it with a release that has no new audio, so do not use it if you want to keep this one.",
  "import.merge.warning.upgrade_can_replace.title": "This film is still monitored",
  "import.merge.warning.upgrade_can_replace":
    "A better release without the new audio can replace the merged file. To stop that, stop monitoring the film in {app}.",
  "import.merge.warning.unknown.title": "The merged file can still be replaced",
  "import.merge.warning.unknown":
    "Something in {app} can still replace the merged file (code: {code}).",
  "import.unmonitored.merged":
    "A merge replaced the file, so the film is now unmonitored: automatic upgrades will not replace it. Pressing the film’s own Search button in {app} still can, so do not use it if you want to keep this one.",
  "import.version.running":
    "Fukku is placing the version next to the film. This page updates by itself.",
  "import.version.done.title": "The second version of {title} is in place",
  "import.version.done.body": "It is kept as {name}, labelled {label}.",
  "import.version.status.placing": "The file is being placed.",
  "import.version.status.linked": "The file is in place. The media server groups it next.",
  "import.version.status.grouped": "The media server lists it with the film.",
  "import.version.status.waiting":
    "The media server has not grouped it yet. Fukku tries again by itself.",
  "import.version.status.film_deleted":
    "Radarr no longer has this film. The version stays on disk.",
  "import.version.status.missing": "The version file is no longer on disk.",
  "import.version.status.unknown": "State of the version: {status}.",
  "import.seasonDub.subtitlesOnly.title": "Nothing was imported",
  "import.seasonDub.subtitlesOnly":
    "You chose to only add subtitles for the episodes that already have a file. The subtitles went to the subtitle manager, and the files stay in this upload until it expires.",
  "import.seasonDub.subtitlesOnly.mixed":
    "For the episodes that already had a file, you chose to only add subtitles: those files were not replaced.",
  "import.seasonDub.replace.one":
    "Your files replaced the ones already in the library for season {seasons}.",
  "import.seasonDub.replace.other":
    "Your files replaced the ones already in the library for seasons {seasons}.",
  "confirm.dub.title": "Episodes that already have a file",
  "confirm.dub.intro":
    "Some episodes of this upload are already in the library. Choose what to do with those. The other episodes are imported as usual.",
  "confirm.dub.season.one":
    "Season {season}: files already exist for {existing} of {count} episode.",
  "confirm.dub.season.other":
    "Season {season}: files already exist for {existing} of {count} episodes.",
  "confirm.dub.legend": "What should happen to them?",
  "confirm.dub.replace": "Replace the files",
  "confirm.dub.replace.body":
    "Your files are imported over the ones in the library, and the season is left unmonitored so automatic upgrades do not replace them. The Search button of an episode in {app} still can.",
  "confirm.dub.subtitlesOnly": "Only add subtitles",
  "confirm.dub.subtitlesOnly.body":
    "Nothing is imported. The subtitles of your upload go to the subtitle manager for the episodes that already have a file.",
  "confirm.dub.unknown": "Another option ({policy})",
  "confirm.dub.unknown.body": "The server offers this, but this page does not know it yet.",
  "confirm.dub.decided": "This server settles it by itself: {option}.",
  "confirm.dub.none": "Nothing is offered for these episodes on this server, so they are left out.",
  "confirm.dub.missing": "Choose what to do with the episodes that already have a file.",
  "confirm.dub.subtitlesOnly.count":
    "Nothing will be imported. The subtitles go to the subtitle manager.",
  "confirm.dub.shared.title": "Replacing these files would remove other episodes’ files",
  "confirm.dub.shared.body":
    "These files each hold several episodes, and some of them are not in this upload. Replacing the file removes those episodes from the library too.",
  "confirm.dub.shared.accept": "Replace {name} anyway",
  "flow2.reason.media_server_too_old":
    "the media server is a Jellyfin older than 12.4, which does not group episode versions.",
  "flow2.reason.media_server_unknown": "the media server’s version could not be read.",
  "flow2.reason.series_folder_unusable":
    "Sonarr’s folder name for the series cannot be part of a file name.",
  "flow2.reason.season_folder_unavailable":
    "a season folder cannot take the versions (missing, not writable, or on another disk), or a required marker file is missing.",
  "confirm.dub.name.replace": "Replace the files",
  "confirm.dub.name.subtitlesOnly": "Only add subtitles",
  "confirm.dub.name.version": "Keep as second versions",
  "import.merge.warning.language_unknown_to_radarr.title": "Radarr does not know the new language",
  "import.merge.warning.language_unknown_to_radarr":
    "{app} has no language for the added tracks, so it lists the merged file with the existing languages only. The tracks are tagged in the file, but custom formats and searches by language in {app} do not see them.",
  "confirm.dub.version": "Keep as second versions",
  "confirm.dub.version.body":
    "Each of these episodes keeps its own file, and your file is kept next to it as a second version. The media server lists both under the episode. {app} does not track the second files.",
  "flow2.version.problem.label_episode":
    "The label must not contain a season or episode number (such as S02E05, 2x05 or E03): the media server would read the file as another episode.",
  "flow2.version.notice.untracked_by_sonarr":
    "Sonarr does not track the second files. It never renames or upgrades them, an upgrade of an episode replaces only its own file, and deleting the series with its files deletes the versions too.",
  "flow2.version.notice.adopted_without_episode_file":
    "If an episode’s own file is deleted, Sonarr takes the second file for the episode’s file on its next scan.",
  "confirm.version.title": "Second versions",
  "confirm.version.label": "Version label",
  "confirm.version.label.hint":
    "Shown with each episode in the media server, for example Español. Do not use quality words such as 1080p or WEB-DL, or a season or episode number.",
  "confirm.version.rows": "Episodes kept as second versions",
  "confirm.version.row.episode": "Season {season}, episode {episode}",
  "confirm.version.row.waiting": "The file name is set from this label when you confirm.",
  "confirm.version.row.double_episode":
    "This file holds several episodes, so it cannot be a second version.",
  "confirm.version.row.shared_file":
    "The episode’s file in the library also holds other episodes, so this cannot be a second version.",
  "confirm.version.row.no_file": "Sonarr lists no file for this episode.",
  "confirm.version.row.outside_library":
    "The episode’s folder is outside the library folders Fukku may use.",
  "confirm.version.row.name_too_long": "The file name would be too long. Use a shorter label.",
  "confirm.version.row.version_exists":
    "A file or an earlier version with this name already exists. Use another label.",
  "confirm.version.row.name_unreadable":
    "The file name would not read as exactly this episode. Ask whoever runs Fukku.",
  "confirm.version.row.folder_unreadable": "Fukku cannot read the episode’s folder.",
  "confirm.version.row.unknown": "This file cannot be a second version (code: {code}).",
  "confirm.version.count.one": "{count} episode is kept as a second version",
  "confirm.version.count.other": "{count} episodes are kept as second versions",
  "confirm.version.only.one":
    "Nothing new will be imported. {count} episode is kept as a second version.",
  "confirm.version.only.other":
    "Nothing new will be imported. {count} episodes are kept as second versions.",
  "confirm.version.refused.title": "These files cannot be kept as second versions",
  "confirm.version.refused.leaveOut.hint.one":
    "You can leave this file out. It gets no second version and its episode keeps its own file. Then press Import again to confirm the rest.",
  "confirm.version.refused.leaveOut.hint.other":
    "You can leave these files out. They get no second version and their episodes keep their own files. Then press Import again to confirm the rest.",
  "confirm.version.leavingOut": "Leaving the files out…",
  "confirm.version.leftOut.done.one": "{count} file left out. Press Import to confirm again.",
  "confirm.version.leftOut.done.other": "{count} files left out. Press Import to confirm again.",
  "confirm.version.refused.leaveOut.one": "Leave out {count} file",
  "confirm.version.refused.leaveOut.other": "Leave out {count} files",
  "confirm.state.version": "Second version",
  "confirm.version.kept":
    "This episode already has a file. Your file is kept next to it as a second version.",
  "confirm.version.leaveOut": "Leave out of the second versions",
  "confirm.version.takeBack": "Keep as a second version again",
  "confirm.version.leftOut":
    "No second version is made for this episode. Its own file stays as it is.",
  "confirm.version.refused.hint":
    "Choose another option above, change the label, or go back and change the match.",
  "confirm.version.refused.hint.fixed":
    "This server settles it by itself. Change the label, or go back and change the match.",
  "jobError.version_upgrade":
    "Sonarr would take every second version for the episodes’ own files, so none was placed. The library was not changed.",
  "jobError.version_unchecked":
    "Sonarr gave no verdict on any second version, so none was placed. The library was not changed.",
  "jobError.version_exists":
    "Every second version already exists under its name, so none was placed. The library was not changed.",
  "import.episodeVersion.partial.title": "{placed} of {total} second versions are in place",
  "import.episodeVersion.partial.body.one": "{count} could not be placed. See each version below.",
  "import.episodeVersion.partial.body.other":
    "{count} could not be placed. See each version below.",
  "confirm.version.label.proposed": "When this is empty, the server uses “{label}”.",
  "confirm.version.refusal.version_upgrade":
    "Sonarr would take this file for an upgrade of the episode’s own file on its next scan.",
  "confirm.version.refusal.version_unchecked":
    "Sonarr gives no verdict on this file: it does not offer it, or reads it as another episode.",
  "confirm.version.refusal.version_unavailable": "This file cannot be a second version.",
  "confirm.version.refusal.version_exists":
    "A file or an earlier version with this name already exists.",
  "import.row.version": "Second version",
  "import.step.mediaServerEpisodeVersions":
    "Group the second versions with the episodes in the media server",
  "import.step.mediaServerEpisodeVersions.failed":
    "The media server did not group the versions with the episodes. Fukku tries again by itself; until then the two show as separate entries.",
  "import.episodeVersion.title": "Second versions",
  "import.episodeVersion.label": "Label: {label}",
  "import.episodeVersion.item": "S{season}E{episode}",
  "import.episodeVersion.done.title": "The second versions of {title} are in place",
  "import.episodeVersion.done.body.one": "{count} file is kept next to its episode’s own file.",
  "import.episodeVersion.done.body.other":
    "{count} files are kept next to their episodes’ own files.",
  "import.episodeVersion.status.placing": "The file is being placed.",
  "import.episodeVersion.status.linked": "The file is in place. The media server groups it next.",
  "import.episodeVersion.status.grouped": "The media server lists it with the episode.",
  "import.episodeVersion.status.waiting":
    "The media server has not grouped it yet. Fukku tries again by itself.",
  "import.episodeVersion.status.missing": "The version file is no longer on disk.",
  "import.episodeVersion.status.refused": "Not placed.",
  "import.episodeVersion.status.adopted":
    "Sonarr now tracks this file as the episode’s own, so it can rename, upgrade or delete it.",
  "import.episodeVersion.status.adoptable":
    "Sonarr would take this file for the episode’s own on its next scan.",
  "import.episodeVersion.status.episode_file_missing":
    "The episode’s own file is gone, so Sonarr takes this one for it on its next scan.",
  "import.episodeVersion.status.series_moved":
    "Sonarr moved the series without this file. The version stays where it was.",
  "import.episodeVersion.status.series_deleted":
    "Sonarr no longer has this series. The version stays on disk; remove it by hand.",
  "import.episodeVersion.status.unknown": "State of the version: {status}.",
  "import.episodeVersion.refusal.version_upgrade":
    "Sonarr would take this file for the episode’s own, so it was not placed.",
  "import.episodeVersion.refusal.version_unchecked":
    "Sonarr gave no verdict on this file, so it was not placed.",
  "import.episodeVersion.refusal.version_exists":
    "A file with this name already exists, so nothing was replaced.",
  "import.episodeVersion.refusal.version_failed":
    "It could not be placed. Tell the person who runs the server.",
  "import.episodeVersion.refusal.unknown": "It was not placed (code: {code}).",
  "errors.version_upgrade":
    "Sonarr would take some of these files for the episodes’ own files, so they cannot be kept as second versions.",
  "errors.version_unchecked":
    "Sonarr gives no verdict on some of these files, so they cannot be kept as second versions.",
};

export default en;
export type Catalogue = typeof en;
