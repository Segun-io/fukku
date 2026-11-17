import type en from "./en";

// Spanish. Typed against the English catalogue: a missing or extra key does not compile.
// Spanish plurals use `one`, `many` (millions) and `other`; `other` covers the rest.
const es: Record<keyof typeof en, string> = {
  "shell.skip": "Saltar al contenido",
  "shell.language": "Idioma",
  "shell.nav.label": "Principal",
  "shell.nav.design": "Patrones",
  "shell.loading": "Cargando…",
  "shell.insecure.title": "La autenticación está desactivada",
  "shell.insecure.body":
    "Este servidor funciona en modo de desarrollo inseguro: cualquiera que llegue a él es administrador. No lo uses nunca con archivos reales.",
  "shell.offline.title": "Sin conexión",
  "shell.offline.body":
    "Las subidas esperan y continúan solas cuando vuelve la conexión. Deja esta página abierta.",
  "shell.signedOut.title": "Vuelve a iniciar sesión",
  "shell.signedOut.body":
    "Tu sesión terminó. No se pierde nada: las subidas continúan donde se quedaron en cuanto inicies sesión.",
  "shell.signedOut.action": "Iniciar sesión de nuevo",
  "shell.signedOut.hint": "Se abre en otra pestaña. Vuelve a esta después.",
  "shell.error.title": "No se pudo cargar esta página",
  "shell.error.body": "El servidor no respondió. Revisa la conexión y vuelve a intentarlo.",
  "shell.error.retry": "Intentar de nuevo",
  "shell.error.detail": "Detalle técnico",
  "shell.notFound.title": "Esta página no existe",
  "shell.notFound.body":
    "Puede que la dirección esté mal escrita o que lo que mostraba se haya borrado.",
  "shell.notFound.action": "Ir a la página de inicio",

  // ---- patterns: file progress ---------------------------------------------------------
  "patterns.progress.of": "{sent} de {total}",
  "patterns.progress.left": "quedan unos {time}",
  "patterns.progress.valueText": "{percent} subido",
  "patterns.progress.status.queued": "Esperando su turno",
  "patterns.progress.status.uploading": "Subiendo",
  "patterns.progress.status.paused": "En pausa",
  "patterns.progress.status.reconnecting": "Esperando la conexión",
  "patterns.progress.status.failed": "La subida se detuvo",
  "patterns.progress.status.done": "Subido",
  "patterns.progress.status.unfinished": "Sin terminar",
  "patterns.progress.hint.paused": "Se detuvo en {sent}. Continuará desde ahí.",
  "patterns.progress.hint.reconnecting": "Continuará sola desde {sent}.",
  "patterns.progress.hint.unfinished": "Hay {sent} en el servidor. Continuará desde ahí.",
  "patterns.progress.hint.resumed": "Continuó desde {sent}. Nada se envía dos veces.",
  "patterns.progress.hint.slow":
    "La conexión es lenta. Deja la página abierta; si se corta, la subida continúa donde se quedó.",
  "patterns.progress.pause": "Pausar",
  "patterns.progress.pause.label": "Pausar {name}",
  "patterns.progress.resume": "Reanudar",
  "patterns.progress.resume.label": "Reanudar {name}",
  "patterns.progress.retry": "Intentar de nuevo",
  "patterns.progress.retry.label": "Intentar de nuevo {name}",
  "patterns.progress.remove": "Quitar",
  "patterns.progress.remove.label": "Quitar {name}",

  // ---- patterns: status timeline -------------------------------------------------------
  "patterns.timeline.label": "Lo que ha pasado hasta ahora",
  "patterns.flow.label": "Pasos de la importación",
  "patterns.flow.step.match": "Elegir el título",
  "patterns.flow.step.mapping": "Revisar los archivos",
  "patterns.flow.step.confirm": "Confirmar",
  "patterns.timeline.step.done": "Hecho",
  "patterns.timeline.step.current": "Ahora",
  "patterns.timeline.step.upcoming": "Pendiente",
  "patterns.timeline.failed":
    "Se detuvo aquí y necesita que intervengas: inténtalo de nuevo o cancela.",
  "patterns.timeline.stalled": "Está en espera aquí: algo no respondió. Continuará solo.",
  "patterns.timeline.cancelled": "Cancelado aquí.",
  "patterns.state.uploading": "Subiendo",
  "patterns.state.uploaded": "Subida completa",
  "patterns.state.probing": "Revisando los archivos",
  "patterns.state.probed": "Archivos revisados",
  "patterns.state.matched": "Título encontrado",
  "patterns.state.awaiting-confirmation": "Esperando tu confirmación",
  "patterns.state.importing": "Añadiendo a la biblioteca",
  "patterns.state.imported": "En la biblioteca",
  "patterns.state.post-processing": "Últimos pasos: subtítulos y servidor multimedia",
  "patterns.state.done": "Listo",

  // ---- patterns: confirmation ----------------------------------------------------------
  "patterns.confirm.warnings": "Antes de confirmar",
  "patterns.confirm.ack": "Confirma que lo has leído",
  "patterns.confirm.ackMissing": "Marca la casilla de arriba para continuar.",
  "patterns.confirm.working": "Trabajando…",

  // ---- patterns: file list -------------------------------------------------------------
  "patterns.files.label": "Archivos de este envío",
  "patterns.files.kind.video": "Video",
  "patterns.files.kind.subtitle": "Subtítulos",
  "patterns.files.kind.archive": "Archivo comprimido",
  "patterns.files.status.ok": "Aceptado",
  "patterns.files.status.pending": "Aún sin revisar",
  "patterns.files.status.refused": "Rechazado",
  "patterns.files.from": "De {archive}",
  "patterns.files.refusedNote":
    "Un archivo rechazado no se conserva. Los demás archivos no se ven afectados.",

  // ---- the /design page (development builds only) ------------------------------------
  "patterns.demo.title": "Patrones",
  "patterns.demo.intro":
    "Las piezas con las que se construyen las páginas de subida y de envíos, con datos de muestra fijos. Esta página solo existe en las compilaciones de desarrollo.",
  "patterns.demo.progress.title": "Progreso de un archivo",
  "patterns.demo.progress.live":
    "Una fila en marcha, para ver cambiar las cifras sin que nada se mueva",
  "patterns.demo.progress.failed": "El servidor rechazó el archivo: pesa más de 80 GB.",
  "patterns.demo.timeline.title": "Línea de estado",
  "patterns.demo.timeline.running": "En marcha",
  "patterns.demo.timeline.failed": "Fallido",
  "patterns.demo.timeline.stalled": "En espera",
  "patterns.demo.timeline.cancelled": "Cancelado",
  "patterns.demo.timeline.done": "Terminado",
  "patterns.demo.timeline.failedDetail": "El proceso de trabajo no arrancó en 10 minutos.",
  "patterns.demo.timeline.stalledDetail": "Radarr no responde.",
  "patterns.demo.timeline.cancel": "Cancelar este envío",
  "patterns.demo.confirm.title": "Confirmación con avisos",
  "patterns.demo.confirm.heading": "¿Añadir esta película a la biblioteca?",
  "patterns.demo.confirm.film": "Película",
  "patterns.demo.confirm.file": "Archivo",
  "patterns.demo.confirm.quality": "Calidad",
  "patterns.demo.confirm.audio": "Audio",
  "patterns.demo.confirm.audioValue": "Español (Latinoamérica), inglés",
  "patterns.demo.confirm.warn1":
    "Radarr ya tiene un archivo de esta película. Al confirmar se reemplaza; el archivo anterior se borra.",
  "patterns.demo.confirm.warn2":
    "La subida es 1080p y el archivo de la biblioteca es 2160p. Para Radarr esto es bajar de calidad.",
  "patterns.demo.confirm.action": "Añadir a la biblioteca",
  "patterns.demo.confirm.back": "Elegir otra película",
  "patterns.demo.confirm.result": "Confirmado. En una página real, aquí empieza la importación.",
  "patterns.demo.files.title": "Lista de archivos",
  "patterns.demo.files.refused1":
    "No se encontró video dentro de este archivo. Puede estar dañado o tener otro nombre.",
  "patterns.demo.files.refused2":
    "Este tipo de archivo no se acepta: solo video, subtítulos y zip.",
  "patterns.demo.picker.title": "Selector de archivos",
  "patterns.demo.picker.full": "Nada elegido todavía",
  "patterns.demo.picker.compact": "Con archivos debajo",
  "patterns.demo.copy.title": "Campo para copiar",
  "patterns.demo.select.title": "Campo de selección",
  "patterns.demo.choice.title": "Elección con consecuencias",
  "patterns.demo.compare.title": "Dos cosas lado a lado",
  "patterns.demo.nudge.title": "Campo con botones de ajuste",
  "patterns.demo.status.title": "Estado de una fila",
  "patterns.demo.flow.title": "Encabezado de un paso",
  "patterns.demo.shell.title": "Estados de la aplicación",
  "patterns.demo.shell.banner": "Aviso de desarrollo inseguro",
  "patterns.demo.shell.offline": "Sin conexión",
  "patterns.demo.shell.signedOut": "Sesión terminada",
  "patterns.demo.shell.empty": "Vacío",
  "patterns.demo.shell.error": "Error",
  "patterns.demo.shell.loading": "Cargando",
  "patterns.demo.shell.nav": "Navegación del encabezado",
  "patterns.demo.empty.title": "Todavía no has subido nada",
  "patterns.demo.empty.body":
    "Las películas y temporadas que subas aparecen aquí, con lo que pasa con cada una.",
  "patterns.demo.empty.action": "Subir un archivo",
  "patterns.demo.error.detail": "GET /api/jobs: 502 Bad Gateway",
  "nav.upload": "Subir",
  "nav.jobs": "Tus envíos",
  "nav.admin": "Administración",

  // ---- admin: salud y ajustes (solo lectura) ------------------------------------------
  "admin.title": "Administración",
  "admin.intro":
    "Cómo va el servidor y los ajustes con los que funciona. Aquí no se puede cambiar nada.",
  "admin.health.title": "Estado de salud",
  "admin.health.refresh": "Revisar de nuevo",
  "admin.health.checked": "Revisado {date}",
  "admin.health.version": "Fukku {version}",
  "admin.health.error.title": "No se pudo cargar el informe de salud",
  "admin.status.ready": "Todo funciona",
  "admin.status.degraded": "Funciona, pero una aplicación no responde",
  "admin.status.not_ready": "No está listo: algo falla en este servidor",
  "admin.checks.title": "Verificaciones",
  "admin.check.ok": "Correcta",
  "admin.check.failing": "Fallida",
  "admin.check.database": "Base de datos",
  "admin.check.spool": "Carpeta de intercambio",
  "admin.check.sentinels": "Almacenamiento montado",
  "admin.check.intakeRoot": "Carpeta de entrada",
  "admin.check.sameFilesystem": "Mismo sistema de archivos que la biblioteca",
  "admin.check.workerHeartbeat": "Proceso de trabajo",
  "admin.check.radarr": "Radarr",
  "admin.check.sonarr": "Sonarr",
  "admin.apps.title": "Aplicaciones",
  "admin.apps.version": "Versión {version}",
  "admin.apps.noVersion": "Sin versión: la aplicación no respondió",
  "admin.apps.none": "Radarr y Sonarr no están configurados.",
  "admin.worker.title": "Proceso de trabajo",
  "admin.worker.age": "Última señal de vida hace {age}. Es un problema a partir de {max}.",
  "admin.worker.none":
    "Sin señal de vida. El proceso de trabajo no está en marcha o no puede escribir en la carpeta de intercambio.",
  "admin.jobs.title": "Envíos por estado",
  "admin.jobs.total.one": "{count} envío en total, de todos los usuarios",
  "admin.jobs.total.other": "{count} envíos en total, de todos los usuarios",
  "admin.jobs.none": "No se pudieron contar los envíos.",
  "admin.disk.title": "Espacio en disco",
  "admin.disk.free": "{free} libres en la carpeta de entrada",
  "admin.disk.above": "Es más que los {margin} que se mantienen libres, así que se aceptan envíos.",
  "admin.disk.below":
    "Es menos que los {margin} que se mantienen libres, así que se rechazan los envíos nuevos.",
  "admin.disk.unknown":
    "No se puede medir el espacio libre en este sistema. El margen es {margin}.",
  "admin.settings.title": "Ajustes",
  "admin.settings.intro":
    "Se leen del archivo de configuración y del entorno. Para cambiar uno, edita esos y reinicia. Los secretos nunca se muestran.",
  "admin.settings.onlySet": "Solo los ajustes que no tienen el valor predeterminado",
  "admin.settings.error.title": "No se pudieron cargar los ajustes",
  "admin.settings.none": "Todos los ajustes tienen su valor predeterminado.",
  "admin.setting.default": "Predeterminado",
  "admin.setting.set": "Definido",
  "admin.setting.empty": "Vacío",
  "admin.setting.hidden": "Definido, oculto",
  "admin.setting.notSet": "Sin definir",
  "admin.setting.env": "Entorno: {name}",

  "upload.title": "Subir",
  "upload.intro":
    "Elige los archivos de la película, la serie o los subtítulos que quieres enviar. Los archivos grandes no son un problema: si se corta la conexión o recargas la página, la subida continúa desde donde se quedó.",
  "upload.restoring": "Revisando tu subida sin terminar…",
  "upload.drop.title": "Elige tus archivos",
  "upload.drop.hint": "Elige varios archivos, o un zip, o suéltalos aquí.",
  "upload.drop.accepted": "Se aceptan: {types}",
  "upload.choose": "Elegir archivos",
  "upload.files.title": "Tus archivos",
  "upload.summary.one": "{done} de {count} archivo subido",
  "upload.summary.other": "{done} de {count} archivos subidos",
  "upload.start": "Empezar a subir",
  "upload.starting": "Empezando…",
  "upload.submitting": "Llegaron todos los archivos. Terminando el envío…",
  "upload.submitFailed.title": "Los archivos llegaron, pero no se pudo terminar el envío",
  "upload.discard": "Descartar este envío",
  "upload.pausedSignedOut":
    "En pausa hasta que vuelvas a iniciar sesión. Continúa desde donde se quedó.",
  "upload.discard.confirm": "¿Descartar este envío? Lo que ya se subió se borra.",
  "upload.startOver": "Empezar de nuevo",
  "upload.remove.confirm": "¿Quitar {name}? Lo que ya se subió se borra.",
  "upload.rejected.title": "Algunos archivos no se añadieron",
  "upload.rejected.dismiss": "Ocultar esta lista",
  "upload.reason.file_mismatch":
    "Este no es el archivo que se estaba subiendo: el nombre, el tamaño o la fecha son distintos. Elige el archivo original.",
  "upload.resume.title": "Tu subida se interrumpió",
  "upload.resume.body":
    "Elige de nuevo los mismos archivos. Cada uno continúa desde donde lo tiene el servidor y no se envía nada dos veces.",
  "upload.large.title": "Archivo muy grande",
  "upload.large.body":
    "Los archivos muy grandes se suben mejor desde casa o con otra conexión rápida. Si tienes que usar esta, mantén abierta la página; si se corta, la subida continúa desde donde se quedó.",
  "upload.jobGone.title": "Este envío no puede continuar",
  "upload.jobGone.body": "{reason} Empieza de nuevo para enviar los archivos otra vez.",

  "jobs.title": "Tus envíos",
  "jobs.list": "Envíos",
  "jobs.count.one": "{count} envío",
  "jobs.count.other": "{count} envíos",
  "jobs.files.one": "{count} archivo",
  "jobs.files.other": "{count} archivos",
  "jobs.unnamed": "Envío sin nombre",
  "jobs.filter.state": "Estado",
  "jobs.filter.allStates": "Todos los estados",
  "jobs.filter.scope": "Mostrar",
  "jobs.scope.mine": "Los míos",
  "jobs.scope.everyone": "De todos",
  "jobs.owner": "De {owner}",
  "jobs.started": "Empezó {age}",
  "jobs.expires": "Se borra el {date}",
  "jobs.empty.title": "Todavía no has subido nada",
  "jobs.empty.body":
    "Las películas y temporadas que subas aparecen aquí, con lo que pasa con cada una.",
  "jobs.empty.action": "Subir un archivo",
  "jobs.emptyFiltered.title": "Ningún envío coincide",
  "jobs.emptyFiltered.body": "Prueba con otro estado, o muestra todos.",
  "jobs.more": "Mostrar más",
  "jobs.error.title": "No se pudieron cargar tus envíos",

  "job.title": "Envío",
  "job.back": "Volver a tus envíos",
  "job.notFound.title": "No se encontró este envío",
  "job.notFound.body": "Puede que se haya borrado, o que sea de otra persona.",
  "job.error.title": "No se pudo cargar este envío",
  "job.owner": "Enviado por {owner}",
  "job.size": "Tamaño: {size}",
  "job.started": "Empezó {date}",
  "job.expires": "Se conserva hasta el {date}; después se borra solo.",
  "job.progress": "Progreso",
  "job.files": "Archivos",
  "job.files.none": "Todavía no hay archivos.",
  "job.uploads.title": "Subidas sin terminar",
  "job.uploads.item": "{done} de {total}",
  "job.uploads.hint":
    "Elige de nuevo los mismos archivos en la página Subir y continúan desde donde se quedaron.",
  "job.uploads.continue": "Seguir subiendo",
  "job.probe.title": "Lo que se encontró en {name}",
  "job.probe.duration": "Duración",
  "job.probe.video": "Video",
  "job.probe.audio": "Audio",
  "job.probe.subtitles": "Subtítulos",
  "job.probe.none": "Ninguno",
  "job.probe.noLanguage": "Idioma sin indicar",
  "job.probe.rate": "{rate} fps",
  "job.probe.flag.default": "predeterminada",
  "job.probe.flag.forced": "forzada",
  "job.probe.flag.hearingImpaired": "para personas con dificultades auditivas",
  "job.skipped.title": "Lo que quedó fuera de {name}",
  "job.skipped.note": "Estos elementos no se extrajeron. Todo lo demás sí.",
  "job.skipped.item": "{name}: {reason}",
  "job.manual.title": "Impórtalo a mano",
  "job.manual.intro":
    "O hazlo tú en Radarr o Sonarr: abre Importación manual allí y elige la carpeta que se muestra aquí.",
  "job.manual.folder": "Carpeta en {app}",
  "job.manual.copy": "Copiar",
  "job.manual.copyFor": "Copiar la carpeta de {app}",
  "job.manual.copied": "Copiado",
  "job.manual.copyFailed": "No se pudo copiar. Selecciona el texto de la carpeta y cópialo.",
  "job.manual.open": "Abrir {app}",
  "job.actions": "Lo que puedes hacer",
  "job.action.cancel": "Cancelar el envío",
  "job.action.retry": "Intentarlo de nuevo",
  "job.action.delete": "Borrar",
  "job.confirm.back": "Dejarlo como está",
  "job.confirm.upload": "Envío",
  "job.confirm.files": "Archivos",
  "job.confirm.size": "Tamaño",
  "job.confirm.cancel.title": "¿Cancelar este envío?",
  "job.confirm.cancel.warning":
    "Los archivos de este envío se borran del servidor. No se puede deshacer.",
  "job.confirm.cancel.importWarning":
    "La importación puede seguir en marcha en la aplicación: aún puede terminar y poner los archivos en la biblioteca. Revisa el título en la aplicación después.",
  "job.confirm.retry.title": "¿Intentar este envío de nuevo?",
  "job.confirm.delete.title": "¿Borrar este envío?",
  "job.confirm.delete.warning": "El envío y sus archivos se borran para siempre.",

  "state.uploading": "Subiendo",
  "state.uploaded": "Subido",
  "state.probing": "Revisando",
  "state.probed": "Revisado",
  "state.matched": "Identificado",
  "state.awaiting-confirmation": "Esperando tu confirmación",
  "state.importing": "Importando",
  "state.imported": "Importado",
  "state.post-processing": "Terminando",
  "state.done": "Listo",
  "state.failed": "Fallido",
  "state.stalled": "En espera",
  "state.cancelled": "Cancelado",

  "stateHint.uploading": "Los archivos todavía se están enviando.",
  "stateHint.uploaded": "Llegaron todos los archivos. Ahora empieza la revisión.",
  "stateHint.probing": "Revisando lo que hay dentro de los archivos.",
  "stateHint.probed": "Los archivos están revisados y listos.",
  "stateHint.matched": "Se encontró la película o la serie.",
  "stateHint.awaiting-confirmation": "Esperando a que confirmes.",
  "stateHint.importing": "Añadiéndose a la biblioteca.",
  "stateHint.imported": "Añadido a la biblioteca.",
  "stateHint.post-processing": "Terminando: subtítulos y servidor multimedia.",
  "stateHint.done": "Terminado.",
  "stateHint.failed": "Se detuvo y necesita que intervengas.",
  "stateHint.stalled": "Esperando algo ajeno a Fukku. Continúa solo.",
  "stateHint.cancelled": "Cancelado. Los archivos se borraron.",

  "jobError.worker_unavailable":
    "La parte de Fukku que revisa los archivos no responde. El envío continúa solo cuando vuelva.",
  "jobError.free_space":
    "No hay espacio libre suficiente en el servidor para abrir un zip. El envío continúa solo cuando haya espacio. Si tarda mucho, avisa a quien administra el servidor.",
  "jobError.task_timeout":
    "La revisión tardó demasiado y se detuvo. Inténtalo de nuevo; si vuelve a fallar, puede que el archivo esté dañado.",
  "jobError.task_failed":
    "Falló la revisión de los archivos. Inténtalo de nuevo; si vuelve a fallar, avisa a quien administra el servidor.",
  "jobError.no_usable_files":
    "Ninguno de los archivos se pudo usar. La lista de abajo dice por qué se rechazó cada uno.",
  "jobError.unknown":
    "Algo salió mal con este envío (código: {code}). Inténtalo de nuevo, o avisa a quien administra el servidor.",

  "refusal.not_a_zip": "No es un archivo zip válido, o está dañado.",
  "refusal.too_many_entries": "El zip tiene demasiados archivos dentro.",
  "refusal.too_large": "Al abrirlo, el zip sería demasiado grande.",
  "refusal.ratio": "El zip está comprimido de forma sospechosa y no se abrió.",
  "refusal.free_space": "No había espacio suficiente para abrirlo.",
  "refusal.extension": "Este tipo de archivo no se acepta: solo video, subtítulos y zip.",
  "refusal.no_video_stream":
    "No se encontró video dentro de este archivo. Puede estar dañado o tener otro nombre.",
  "refusal.no_duration":
    "El archivo no tiene una duración legible. Puede estar dañado o incompleto.",
  "refusal.not_text": "Este archivo de subtítulos no es texto legible.",
  "refusal.unreadable": "No se pudo leer el archivo. Puede estar dañado.",
  "refusal.timeout": "La revisión de este archivo tardó demasiado.",
  "refusal.too_many_streams":
    "Este archivo tiene más pistas de audio, video y subtítulos de las que acepta el servidor.",
  "refusal.result_too_large":
    "El informe de los archivos de esta subida creció demasiado para incluir este archivo. Súbelo en un envío aparte.",
  "refusal.outside_library": "El archivo está fuera de las carpetas que Fukku puede leer.",
  "refusal.unknown": "Se rechazó el archivo (código: {code}).",

  "skip.traversal": "su nombre apunta fuera de la carpeta",
  "skip.absolute": "su nombre es una ruta absoluta",
  "skip.drive_letter": "su nombre empieza por una letra de unidad",
  "skip.backslash": "su nombre contiene una barra invertida",
  "skip.symlink": "es un enlace, no un archivo",
  "skip.special": "es un archivo especial del sistema",
  "skip.nested_archive": "es un zip dentro del zip",
  "skip.extension": "este tipo de archivo no se acepta",
  "skip.name_too_long": "su nombre es demasiado largo",
  "skip.duplicate": "otro archivo ya tiene este nombre",
  "skip.encrypted": "está protegido con contraseña",
  "skip.unknown": "omitido (código: {code})",

  "errors.unknown": "Algo salió mal.",
  "errors.signed_out": "Tu sesión terminó. Vuelve a iniciar sesión.",
  "errors.network": "No se puede conectar con el servidor. Revisa tu conexión.",
  "errors.bad_response": "El servidor envió una respuesta que la página no puede leer.",
  "errors.forbidden": "No tienes permiso para hacer eso.",
  "errors.origin_mismatch": "La solicitud vino de otro sitio web y se rechazó.",
  "errors.not_found": "No se encontró.",
  "errors.bad_request": "La solicitud no es válida.",
  "errors.too_large": "La solicitud es demasiado grande.",
  "errors.internal": "El servidor tuvo un problema. Inténtalo más tarde.",
  "errors.uploads_unfinished": "Algunas subidas todavía no han terminado.",
  "errors.no_files": "Primero añade al menos un archivo.",
  "errors.wrong_state": "No es posible mientras el envío está en este estado.",
  "errors.job_active": "Este envío sigue en marcha.",
  "errors.bad_name": "El nombre del archivo no es válido.",
  "errors.job_not_uploading": "Este envío ya no admite archivos.",
  "errors.duplicate_name": "Ya se añadió un archivo con este nombre.",
  "errors.file_too_large": "El archivo es más grande de lo que permite el servidor.",
  "errors.extension_not_allowed": "Este tipo de archivo no se acepta.",
  "errors.too_many_uploads": "Demasiadas subidas a la vez. Espera a que termine alguna.",
  "errors.too_many_jobs":
    "Tienes demasiados envíos sin terminar. Termina o cancela alguno primero.",
  "errors.in_flight_limit": "El servidor está recibiendo todo lo que puede. Inténtalo más tarde.",
  "errors.insufficient_space": "El servidor no tiene suficiente espacio libre.",
  "errors.storage_unavailable": "El almacenamiento no está disponible ahora. Inténtalo más tarde.",
  "errors.offset_mismatch":
    "Lo que la subida creía haber enviado no coincidía con lo que recibió el servidor. Continúa desde donde está el servidor.",
  "errors.upload_busy":
    "El servidor sigue trabajando en esta subida. Inténtalo de nuevo en un momento.",

  "job.match": "Elegir la película o la serie",
  "job.match.body":
    "Indica de qué título son estos archivos; luego los revisas y los importas. La importación la hace Radarr o Sonarr.",
  "job.target.title": "Título elegido",
  "job.target.body": "{title} ({year}) en {app}",
  "job.target.change": "Elegir otro título",
  "match.title": "Elegir la película o la serie",
  "match.intro":
    "Indica de qué título son estos archivos. Fukku lo añade a Radarr o a Sonarr si es nuevo; no se descarga nada.",
  "match.back": "Volver al envío",
  "match.manual": "Importarlo a mano",
  "match.kind.legend": "¿Qué es?",
  "match.kind.movie": "Una película",
  "match.kind.series": "Una serie",
  "match.kind.hint": "Elegido a partir de los archivos que subiste. Cámbialo si no es correcto.",
  "match.search.label": "Título",
  "match.search.hint":
    "¿No aparece? Prueba el título en inglés o un id: tmdb:<id> para una película, tvdb:<id> para una serie.",
  "match.search.submit": "Buscar",
  "match.results.title": "Resultados",
  "match.results.count.one": "{count} resultado",
  "match.results.count.other": "{count} resultados",
  "match.results.none.title": "No se encontró nada para «{term}»",
  "match.results.none.body":
    "Prueba el título en inglés, un nombre más corto o un id como tmdb:<id> o tvdb:<id>.",
  "match.hit.name": "{title} ({year})",
  "match.hit.inLibrary": "Ya está en {app}",
  "match.hit.hasFile": "Ya tiene un archivo",
  "match.hit.seasons.one": "{count} temporada",
  "match.hit.seasons.other": "{count} temporadas",
  "match.hit.noOverview": "Sin descripción.",
  "match.exists.title": "Esta película ya tiene un archivo",
  "match.exists.body":
    "Si tu envío es otro idioma de esta película, Fukku lo compara con el archivo de la biblioteca. Después puedes fusionar el audio nuevo en ese archivo o conservar tu envío como segunda versión.",
  "match.options.title": "Opciones",
  "match.options.inLibrary": "{title} ya está en {app}. Sus ajustes se quedan como están.",
  "match.profile": "Perfil de calidad",
  "match.profile.choose": "Elige un perfil",
  "match.profile.needed": "Elige un perfil de calidad para continuar.",
  "match.folder": "Carpeta raíz",
  "match.monitored": "Reemplazarla cuando aparezca una copia mejor",
  "match.seriesType": "Tipo de serie",
  "match.seriesType.standard": "Estándar",
  "match.seriesType.anime": "Anime",
  "match.seriesType.daily": "Diaria",
  "match.submit": "Usar este título",
  "match.noApps.title": "Radarr y Sonarr no están configurados",
  "match.noApps.body":
    "Pide a quien administra Fukku que conecte alguno. Mientras tanto puedes importar los archivos a mano.",
  "match.wrongState.title": "Ahora no se puede elegir el título de este envío",
  "match.wrongState.body": "Solo se puede elegir un título cuando los archivos están revisados.",
  "errors.already_in_library":
    "Esta película ya tiene un archivo. Elígela otra vez para añadir otro idioma.",
  "errors.title_in_use": "Otro envío ya está trabajando con este título. Espera a que termine.",
  "errors.title_busy": "Este título se está descargando ahora. Inténtalo de nuevo cuando termine.",
  "errors.arr_unavailable":
    "No se puede conectar con Radarr o Sonarr ahora. Inténtalo de nuevo en un momento.",
  "match.profile.none": "{app} no tiene perfiles de calidad. Añade uno allí y vuelve.",
  "match.unavailable.title": "Radarr y Sonarr no responden",
  "errors.app_not_configured": "Esa aplicación no está configurada en Fukku.",
  "errors.title_not_found": "La aplicación ya no conoce ese título. Busca de nuevo.",
  "job.mapping": "Revisar los archivos",
  "mapping.title": "Revisar los archivos",
  "mapping.back": "Volver al envío",
  "mapping.target": "{title} ({year}) en {app}",
  "mapping.intro":
    "Cada archivo tiene su fila, con lo que {app} reconoce en él. Cambia lo que esté mal: {app} vuelve a revisar cada cambio.",
  "mapping.fromNames":
    "La temporada y los episodios salen de los nombres de los archivos o de lo que {app} supone. Compáralos con lo que es cada archivo de verdad.",
  "mapping.pending.title": "{app} está revisando los archivos",
  "mapping.pending.body": "Tarda un momento. Las filas aparecen aquí solas.",
  "mapping.wrongState.title": "Los archivos no se pueden revisar ahora",
  "mapping.wrongState.body":
    "Este envío no está esperando que se revisen sus archivos. Abre el envío para ver en qué punto está.",
  "mapping.openJob": "Abrir el envío",
  "mapping.summary.label": "Resumen",
  "mapping.summary.toImport.one": "{count} para importar",
  "mapping.summary.toImport.other": "{count} para importar",
  "mapping.summary.skipped.one": "{count} omitido",
  "mapping.summary.skipped.other": "{count} omitidos",
  "mapping.summary.attention.one": "{count} necesita atención",
  "mapping.summary.attention.other": "{count} necesitan atención",
  "mapping.filter": "Solo los archivos que necesitan atención",
  "mapping.filter.empty": "Ningún archivo necesita atención.",
  "mapping.table.label": "Archivos y su destino",
  "mapping.col.select": "Elegir",
  "mapping.col.file": "Archivo",
  "mapping.col.season": "Temporada",
  "mapping.col.episodes": "Episodios",
  "mapping.col.quality": "Calidad",
  "mapping.col.languages": "Idiomas",
  "mapping.col.notes": "Para revisar",
  "mapping.col.skip": "Omitir",
  "mapping.select.row": "Elegir {name}",
  "mapping.select.all": "Elegir todos los archivos mostrados",
  "mapping.duration": "{actual}, esperado {expected}",
  "mapping.duration.only": "{actual}",
  "mapping.season.for": "Temporada de {name}",
  "mapping.season.option": "Temporada {season}",
  "mapping.season.unknown": "Sin definir",
  "mapping.episode.first": "Episodio de {name}",
  "mapping.episode.second": "Segundo episodio de {name}",
  "mapping.episode.choose": "Elige un episodio",
  "mapping.episode.none": "Sin segundo episodio",
  "mapping.episode.option": "{number}. {title}",
  "mapping.episode.hasFile": "{number}. {title} (ya tiene archivo)",
  "mapping.quality.for": "Calidad de {name}",
  "mapping.quality.none": "No ofrecida",
  "mapping.skip.for": "Omitir {name}",
  "mapping.skip.server": "Omitido por el servidor",
  "mapping.skip.server.hint":
    "El servidor omite esta fila porque su episodio ya tiene un archivo. Marca la casilla para omitirla tú.",
  "mapping.skip.label": "Omitir",
  "mapping.checking": "Revisando…",
  "mapping.failed": "El cambio no se revisó.",
  "mapping.failed.wrongState":
    "Los archivos cambiaron mientras editabas y la lista se recargó. Repite tu cambio.",
  "mapping.retry": "Intentar de nuevo",
  "mapping.discard": "Deshacer mi cambio",
  "mapping.ok": "Sin problemas.",
  "mapping.state.attention": "Necesita atención",
  "mapping.state.refused": "Rechazado",
  "mapping.state.skipped": "Omitido",
  "mapping.state.failed": "Sin revisar",
  "mapping.languages.none": "Ninguno",
  "mapping.notOffered":
    "{app} no ofreció este archivo, así que no se puede cambiar ni importar. Solo puedes omitirlo.",
  "mapping.rejection.permanent":
    "{app} rechaza este archivo: «{reason}». Para importarlo de todos modos, confírmalo en el paso siguiente, u omítelo aquí.",
  "mapping.rejection.temporary":
    "{app} rechaza este archivo por ahora: «{reason}». Puede cambiar solo. Para importarlo de todos modos, confírmalo en el paso siguiente.",
  "mapping.warning.double_episode":
    "Este archivo dura casi el doble que un episodio. Puede contener dos.",
  "mapping.warning.double_episode.action": "Hacerlo de los episodios {first} y {second}",
  "mapping.warning.runtime_mismatch":
    "Su duración está lejos de la del episodio. La numeración, o el orden de los episodios, puede estar mal.",
  "mapping.warning.duplicate_episode": "Otro archivo tiene asignado el mismo episodio.",
  "mapping.warning.existing_file":
    "Este episodio ya tiene un archivo en {app}. Todavía no se puede importar, así que el archivo se omite.",
  "mapping.warning.duplicate_film":
    "Otro archivo de esta película tampoco está omitido. Una película tiene un solo archivo: omite el que no quieras.",
  "mapping.warning.not_offered": "{app} no ofreció este archivo.",
  "mapping.warning.language":
    "Puede que el audio no esté en el idioma original. Idiomas: {languages}. Puntuación: {score}; el perfil de calidad pide al menos {min}.",
  "mapping.warning.language.formats": "Reglas que coinciden: {formats}.",
  "mapping.warning.unknown": "Algo que revisar ({code}).",
  "mapping.hint.dvd_order.title": "El orden de los episodios puede ser otro",
  "mapping.hint.dvd_order.body":
    "La mayoría de los archivos duran mucho más o mucho menos que sus episodios. Puede que la serie use otro orden (orden DVD). Revisa la numeración.",
  "mapping.hint.unknown": "Una pista de la aplicación ({code}).",
  "mapping.bulk.label": "Cambiar los archivos elegidos",
  "mapping.bulk.selected.one": "{count} archivo elegido",
  "mapping.bulk.selected.other": "{count} archivos elegidos",
  "mapping.bulk.none": "Elige archivos para mover sus episodios o cambiar su temporada a la vez.",
  "mapping.bulk.clear": "Quitar la elección",
  "mapping.bulk.shift": "Mover los episodios en",
  "mapping.bulk.shift.apply": "Mover episodios",
  "mapping.bulk.season": "Temporada",
  "mapping.bulk.season.apply": "Cambiar la temporada",
  "mapping.bulk.checking": "Revisando los archivos elegidos…",
  "mapping.bulk.failed": "El cambio en los archivos elegidos no se revisó.",
  "mapping.continue": "Seguir a la confirmación",
  "mapping.continue.attention":
    "Los archivos que necesitan atención se confirman uno por uno en el paso siguiente.",
  "mapping.continue.none": "Nada que importar. Arregla un archivo o deja de omitirlo primero.",
  "mapping.continue.busy": "Espera a que terminen las revisiones.",

  // ---- confirmation, progress and result (#44) ------------------------------------------
  "job.review": "Revisar e importar",
  "jobError.import_failed":
    "La aplicación no pudo importar todos los archivos. La lista de abajo muestra cuáles llegaron. Revisa la aplicación y vuelve a intentarlo.",
  "jobError.arr_unavailable":
    "Esperando a {app}, que no responde. No tienes que hacer nada: la importación continúa sola cuando vuelva.",
  "jobError.proposal_failed":
    "La aplicación rechazó los archivos o no pudo volver a revisarlos. No se importó nada. Inténtalo de nuevo; si vuelve a fallar, avisa a quien administra el servidor.",
  "jobError.title_busy":
    "La aplicación está descargando este título ahora, así que no se pudieron importar los archivos. Inténtalo de nuevo cuando termine la descarga.",
  "jobError.library_has_file":
    "El título recibió un archivo en la biblioteca después de la confirmación, y esta importación lo reemplazaría, así que no se envió nada. Vuelve a subir los archivos para elegir qué hacer con el archivo que tiene ahora la biblioteca.",
  "errors.needs_override":
    "Algunos archivos tienen problemas que aún no has aceptado. Marca «Importar de todos modos» en cada uno, o déjalos fuera.",
  "errors.admin_required":
    "En este servidor solo un administrador puede hacer este cambio en la biblioteca. Pide a un administrador que confirme este envío, o deja fuera lo que cambiaría la biblioteca.",
  "errors.incomplete_rows":
    "Algunos archivos no tienen película o episodio al que ir. Elige uno para cada uno, o déjalos fuera.",
  "errors.flow2":
    "Esta película ya tiene un archivo en Radarr. Vuelve a elegirla para que Fukku ofrezca fusionar el nuevo idioma o guardarlo como segunda versión.",
  "errors.nothing_to_import": "No hay nada que importar: todos los archivos están fuera.",

  "confirm.title": "Confirmar la importación",
  "confirm.back": "Volver al envío",
  "confirm.error.title": "No se pudo preparar la importación",
  "confirm.wrongState.title": "Este envío no se puede confirmar ahora",
  "confirm.wrongState.body": "Solo se puede confirmar después de elegir una película o serie.",
  "confirm.wrongState.action": "Ir al envío",
  "confirm.pending": "{app} está revisando los archivos. Tarda un momento.",
  "confirm.intro":
    "{app} ya revisó cada archivo. Revisa lo que encontró, corrige lo que esté mal y luego importa.",
  "confirm.target": "Entra en {app} como",
  "confirm.title.name": "{title} ({year})",
  "confirm.rows.title.one": "{count} archivo",
  "confirm.rows.title.other": "{count} archivos",
  "confirm.row.goesTo": "Va a ({app})",
  "confirm.row.episode": "T{season}E{number} {title}",
  "confirm.row.file": "Tu archivo",
  "confirm.row.noDuration": "Duración desconocida",
  "confirm.quality": "Calidad",
  "confirm.languages": "Idiomas",
  "confirm.languages.none": "No se encontró ninguno",
  "confirm.languages.remove": "Quitar {name}",
  "confirm.languages.add": "Añadir un idioma",
  "confirm.languages.choose": "Añadir un idioma…",
  "confirm.formats": "Formatos personalizados",
  "confirm.formats.none": "Ninguno",
  "confirm.score": "Puntuación de formatos personalizados",
  "confirm.checking": "{app} está revisando tu cambio…",
  "confirm.issues.title": "Lee esto antes de importar",
  "confirm.rejection": "{app} rechaza este archivo:",
  "confirm.override": "Importar de todos modos",
  "confirm.state.ready": "Listo para importar",
  "confirm.state.issues": "Necesita tu decisión",
  "confirm.state.agreed": "Se importa de todos modos",
  "confirm.state.out": "Se deja fuera",
  "confirm.count.label": "Lo que va a pasar",
  "confirm.count.in.one": "Se importará {count} archivo",
  "confirm.count.in.other": "Se importarán {count} archivos",
  "confirm.count.out.one": "{count} se deja fuera",
  "confirm.count.out.other": "{count} se dejan fuera",
  "confirm.override.missing":
    "Marca «Importar de todos modos» en cada archivo con un problema, o déjalo fuera.",
  "confirm.override.adminOnly":
    "{app} rechazó este archivo, o reemplazaría un archivo de la biblioteca, así que solo un administrador puede importarlo de todos modos. Déjalo fuera, o pide a un administrador que confirme este envío.",
  "confirm.override.adminMissing":
    "Solo un administrador puede importar archivos que {app} rechazó o que reemplazarían un archivo de la biblioteca. Déjalos fuera para importar el resto, o pide a un administrador que confirme este envío.",
  "confirm.admin.leaveOut.hint.one":
    "Puedes dejar este archivo fuera e importar el resto: déjalo fuera aquí y después elige Importar otra vez.",
  "confirm.admin.leaveOut.hint.other":
    "Puedes dejar estos archivos fuera e importar el resto: déjalos fuera aquí y después elige Importar otra vez.",
  "confirm.override.stale":
    "El servidor encontró problemas que esta página no mostraba. Ahora se muestran: léelos y vuelve a intentarlo.",
  "confirm.skip": "Dejar este archivo fuera",
  "confirm.skip.undo": "Incluir este archivo de nuevo",
  "confirm.skipped": "Este archivo queda fuera y no se importará.",
  "confirm.nothing": "Todos los archivos están fuera, así que no hay nada que importar.",
  "confirm.submit": "Importar",
  "confirm.change": "Elegir otro título",
  "confirm.warning.not_offered":
    "La aplicación no reconoció este archivo, así que no puede importarlo.",
  "confirm.warning.existing_file":
    "Un episodio de este archivo ya tiene un archivo en la biblioteca. Reemplazar archivos llegará en una versión posterior.",
  "confirm.warning.double_episode":
    "Este archivo dura casi el doble que un episodio. Puede tener dos episodios: revisa cuáles abarca.",
  "confirm.warning.runtime_mismatch":
    "Este archivo es mucho más corto o más largo que el episodio al que se asignó. Puede ser el episodio equivocado u otro montaje.",
  "confirm.warning.duplicate_episode":
    "Otro archivo está asignado al mismo episodio. Solo se puede importar uno de los dos.",
  "confirm.warning.duplicate_film":
    "Hay más de un archivo para importar de esta película, y una película tiene un solo archivo. Deja fuera los demás.",
  "confirm.warning.language":
    "Los idiomas de este archivo pueden no ser los que quieres para este título, por ejemplo un doblaje cuando se quiere el original. Revísalos antes de importar.",
  "confirm.warning.language.facts":
    "Formatos personalizados: {formats}. Puntuación {score}; el perfil pide al menos {min}.",
  "confirm.warning.unknown": "La aplicación o Fukku notó algo en este archivo (código: {code}).",
  "confirm.hint.dvd_order":
    "La mayoría de los episodios están lejos de la duración esperada. Puede que los archivos sigan el orden del DVD y no el de emisión: revisa los números de episodio.",
  "confirm.hint.unknown": "Una nota del servidor (código: {code}).",
  "confirm.subtitles.title": "Archivos de subtítulos",
  "confirm.subtitles.intro":
    "Estos archivos se pasan al gestor de subtítulos después de la importación. El idioma y las marcas se deducen del nombre del archivo: corrígelos si están mal.",
  "confirm.subtitles.language": "Idioma de {name} (dos letras, como en o es)",
  "confirm.subtitles.noLanguage": "Escribe el idioma con dos letras, como en o es.",
  "confirm.subtitles.invalid":
    "Da a cada archivo de subtítulos un idioma de dos letras, como en o es.",
  "confirm.subtitles.guessHint": "Deducido del nombre del archivo.",
  "confirm.subtitles.forced": "{name} es forzado",
  "confirm.subtitles.forced.short": "Forzado",
  "confirm.subtitles.hi": "{name} es para personas con dificultades auditivas",
  "confirm.subtitles.hi.short": "Para personas con dificultades auditivas",

  "import.title": "Importando",
  "import.title.done": "Resultado",
  "import.app": "la aplicación",
  "import.running":
    "{app} está moviendo los archivos a la biblioteca. Esta página se actualiza sola.",
  "import.waiting": "La importación espera a {app} y continúa sola.",
  "import.count": "{done} de {total} archivos en la biblioteca",
  "import.rows": "Archivos",
  "import.row": "Archivo {n} de {total}",
  "import.row.waiting": "En espera",
  "import.row.imported": "Importado",
  "import.row.skipped": "Fuera",
  "import.row.failed": "No importado",
  "import.steps": "Pasos después de la importación",
  "import.post.running": "Los archivos ya están en la biblioteca. Estos pasos lo dejan todo listo.",
  "import.step.mediaServerRefresh": "Avisar al servidor multimedia de los archivos nuevos",
  "import.step.mediaServerVersions":
    "Agrupar la segunda versión con la película en el servidor multimedia",
  "import.step.subtitles": "Pasar los subtítulos al gestor de subtítulos",
  "import.step.mediaServerRefresh.failed":
    "El servidor multimedia no mostró el título a tiempo. Aparecerá tras su próximo escaneo de la biblioteca, o inicia uno allí a mano.",
  "import.step.mediaServerVersions.failed":
    "El servidor multimedia no agrupó la versión con la película. Fukku lo intenta de nuevo solo; mientras tanto aparecen como entradas separadas.",
  "import.step.subtitles.failed":
    "Los subtítulos no se escribieron. Añádelos a mano en el gestor de subtítulos. Los archivos siguen en esta subida hasta que caduque.",
  "import.step.failed":
    "Este paso falló. La importación en sí no se deshace. Avisa a quien administra el servidor.",
  "import.status.pending": "En curso",
  "import.status.ok": "Hecho",
  "import.status.failed": "Falló",
  "import.status.skipped": "No se usa",
  "import.done.title": "{title} está en la biblioteca",
  "import.done.body": "{app} importó los archivos.",
  "import.done.failedSteps": "Uno de los pasos siguientes falló: mira abajo.",
  "import.link": "Abrir el título en el servidor multimedia",
  "import.link.none": "No hay enlace al servidor multimedia para este título.",
  "import.unmonitored.title": "Este título no está monitoreado",
  "import.unmonitored.chosen":
    "Elegiste no reemplazar este archivo cuando aparezca una copia mejor, así que las mejoras automáticas no lo reemplazarán. El botón de búsqueda del propio título en {app} sí puede hacerlo.",
  "import.unmonitored.body":
    "Solo se importó un doblaje, así que el título ya no está monitoreado: las mejoras automáticas no reemplazarán este archivo. El botón de búsqueda del propio título en {app} sí puede hacerlo, así que no lo uses si quieres conservar este.",

  // ---- Flow 2: otro idioma de una película que ya está en la biblioteca (#59) ----
  "match.exists.action": "Añadir otro idioma",
  "job.flow2": "Comparar con el archivo de la biblioteca",
  "job.flow2.body":
    "Esta película ya tiene un archivo. Compara tu envío con él y después fusiona el audio nuevo o consérvalo como segunda versión.",
  "flow2.back": "Volver al envío",
  "flow2.title": "Otro idioma de esta película",
  "flow2.subject": "{title} ({year}) en Radarr",
  "flow2.intro":
    "Esta película ya tiene un archivo en Radarr. Fukku compara tu envío con él y después eliges qué hacer.",
  "flow2.error.title": "No se pudo mostrar la comparación",
  "flow2.wrongState.title": "Este envío no se puede comparar ahora",
  "flow2.wrongState.body":
    "Se puede comparar cuando se elige una película que ya tiene un archivo, y hasta que lo confirmes.",
  "flow2.wrongState.action": "Ir al envío",
  "flow2.probed.title": "Elige la película otra vez",
  "flow2.probed.body":
    "No hay ninguna película elegida para este envío, o el archivo de la película en Radarr cambió desde que la elegiste. Elígela otra vez y se hará de nuevo la comparación.",
  "flow2.notFlow2.title": "Esta película no tiene archivo en la biblioteca",
  "flow2.notFlow2.body":
    "No hay nada que comparar. Revisa los archivos e impórtalos como una película nueva.",
  "flow2.pending": "Se está leyendo el archivo de Radarr. Tarda un momento.",
  "flow2.noUpload.title": "No hay ningún video para comparar",
  "flow2.noUpload.body":
    "No se pudo leer ningún video de este envío. Vuelve y revisa sus archivos.",
  "flow2.changed.action": "Elegir la película otra vez",
  "flow2.compare.title": "Tu envío y el archivo de la biblioteca",
  "flow2.side.library": "En la biblioteca",
  "flow2.side.upload": "Tu envío",
  "flow2.file": "Archivo",
  "flow2.size": "Tamaño",
  "flow2.quality": "Calidad",
  "flow2.languages": "Idiomas",
  "flow2.duration": "Duración",
  "flow2.audio": "Audio",
  "flow2.subtitles": "Subtítulos",
  "flow2.unknown": "Desconocido",
  "flow2.seconds": "{value}\u00a0s",
  "flow2.finding.match.title": "El mismo montaje",
  "flow2.finding.match.body":
    "Los dos archivos duran lo mismo, con una diferencia de {delta}. El audio de tu envío se puede añadir al archivo de la biblioteca.",
  "flow2.finding.pal.title": "Una aceleración PAL",
  "flow2.finding.pal.body":
    "Los dos archivos difieren en {delta}, una proporción de {ratio}: la de una película acelerada de 23,976 a 25 fotogramas por segundo. Su audio se iría desincronizando de la imagen del archivo de la biblioteca, así que no se puede fusionar. Se puede conservar como segunda versión.",
  "flow2.finding.different.title": "Un montaje distinto",
  "flow2.finding.different.body":
    "Los dos archivos difieren en {delta} (una proporción de {ratio}) y no como lo haría una aceleración. Puede ser un montaje más largo o más corto de la película. Su audio no se puede fusionar, pero se puede conservar junto al archivo de la biblioteca como segunda versión.",
  "flow2.options.title": "Qué puedes hacer",
  "flow2.offer.merge": "Fusionar el audio en el archivo de la biblioteca",
  "flow2.offer.merge.body":
    "El audio nuevo pasa a ser una pista más del archivo de la biblioteca. Queda un solo archivo, y Radarr reemplaza el suyo por el fusionado.",
  "flow2.offer.version": "Conservarlo como segunda versión",
  "flow2.offer.version.body":
    "Tu envío se guarda junto a la película como otra versión, y el servidor multimedia la muestra con la película. El archivo de la biblioteca no se toca.",
  "flow2.offer.legend": "¿Cómo quieres añadirlo?",
  "flow2.notOffered.title": "No se ofrece",
  "flow2.notOffered.item": "{offer}: {reason}",
  "flow2.reason.merge_disabled": "la fusión está desactivada en este servidor.",
  "flow2.reason.versions_disabled": "las segundas versiones están desactivadas en este servidor.",
  "flow2.reason.versions_unsupported":
    "las segundas versiones necesitan un servidor multimedia que pueda agruparlas (Jellyfin), y no hay ninguno configurado.",
  "flow2.reason.runtime_differs": "fusionar exige el mismo montaje, y este no lo es.",
  "flow2.reason.unknown": "no es posible aquí (código: {code}).",
  "flow2.reason.admin_required":
    "cambia un archivo que la biblioteca ya tiene, así que solo un administrador puede confirmarlo. Un administrador puede abrir este envío y confirmarlo.",
  "flow2.admin.title": "Un administrador debe confirmar este envío",
  "flow2.admin.body":
    "Fusionar en el archivo de la película y conservar una segunda versión cambian la biblioteca, y en este servidor solo un administrador puede hacerlo. No se cambió nada.",
  "flow2.admin.body.merge":
    "Fusionar en el archivo de la película cambia la biblioteca, y en este servidor solo un administrador puede hacerlo. No se cambió nada.",
  "flow2.admin.body.version":
    "Conservar una segunda versión cambia la biblioteca, y en este servidor solo un administrador puede hacerlo. No se cambió nada.",
  "flow2.nothing.title": "Aquí no se puede hacer nada con este envío",
  "flow2.nothing.body":
    "Pide a quien administra Fukku que lo cambie, o importa los archivos a mano. No se cambió nada.",
  "flow2.sync.title": "Revisa que el audio esté sincronizado",
  "flow2.sync.intro":
    "Que duren lo mismo no demuestra que el audio encaje. Fukku hace tres clips cortos con la imagen de la película y el audio nuevo: del principio, de la mitad y del final. En cada uno, fíjate en un momento en que se vea producirse un sonido, como alguien que habla o una puerta que se cierra.",
  "flow2.sync.audio": "Pista de audio que se añade",
  "flow2.sync.track": "{track} ({detail})",
  "flow2.sync.offset": "Desfase en milisegundos",
  "flow2.sync.offset.hint":
    "Déjalo en 0 para los primeros clips. También puedes escribir un número: uno negativo adelanta el audio nuevo y uno positivo lo retrasa. De −60000 a 60000.",
  "flow2.sync.offset.unit": "ms",
  "flow2.sync.offset.late": "El sonido llega después de la imagen",
  "flow2.sync.offset.early": "El sonido llega antes que la imagen",
  "flow2.sync.offset.earlier": "{ms} ms antes",
  "flow2.sync.offset.later": "{ms} ms después",
  "flow2.sync.offset.say.none": "El audio nuevo no se mueve.",
  "flow2.sync.offset.say.later": "El audio nuevo empieza {seconds} después.",
  "flow2.sync.offset.say.earlier": "El audio nuevo empieza {seconds} antes.",
  "flow2.sync.offset.invalid": "Escribe un número entero de −60000 a 60000.",
  "flow2.sync.make": "Crear los clips",
  "flow2.sync.again": "Crear los clips otra vez",
  "flow2.sync.pending": "Creando los clips… Tarda un momento, y la página se actualiza sola.",
  "flow2.sync.failed.title": "No se pudieron crear los clips",
  "flow2.sync.error.outside_library":
    "El archivo de la biblioteca está fuera de las carpetas que Fukku puede leer.",
  "flow2.sync.error.library_unreadable": "No se pudo leer el archivo de la biblioteca.",
  "flow2.sync.error.upload_unreadable": "No se pudo leer el archivo subido.",
  "flow2.sync.error.render_failed": "No se pudieron crear los clips con estos archivos.",
  "flow2.sync.error.timeout": "Crear un clip tardó demasiado.",
  "flow2.sync.error.too_large": "Un clip resultó demasiado grande.",
  "flow2.sync.error.task_failed": "El proceso de trabajo no pudo crear los clips.",
  "flow2.sync.error.task_timeout": "El proceso de trabajo tardó demasiado en crear los clips.",
  "flow2.sync.error.unknown": "No se pudieron crear los clips (código: {code}).",
  "flow2.sync.ready": "Creados con {track} y un desfase de {offset}\u00a0ms.",
  "flow2.sync.clips": "Clips",
  "flow2.sync.clip": "Clip {n} de {total}, desde {at}",
  "flow2.sync.clip.fallback": "Tu navegador no puede reproducir este clip.",
  "flow2.sync.stale":
    "La pista o el desfase cambiaron después de crear estos clips. Créalos otra vez para revisarlo.",
  "flow2.sync.match": "Coinciden, continuar",
  "flow2.sync.nomatch":
    "Si el sonido va adelantado o atrasado lo mismo en todos los clips, cambia el desfase de arriba y crea los clips otra vez.",
  "flow2.sync.drift":
    "Si coincide al principio y no al final, los dos archivos van a velocidades distintas. Un desfase no lo arregla: conserva el envío como segunda versión.",
  "flow2.sync.question": "¿El sonido y la imagen coinciden en todos los clips?",
  "flow2.sync.confirmed": "Revisado con un desfase de {offset}\u00a0ms.",
  "flow2.refusal.title": "La última fusión fue rechazada",
  "flow2.refusal.choice":
    "Usó {track} con un desfase de {offset}\u00a0ms. No se cambió nada en la biblioteca.",
  "flow2.refusal.hint":
    "Elige otro desfase u otra pista de audio, crea los clips otra vez, revísalos y confirma la fusión de nuevo.",
  "flow2.merge.title": "Fusionar en el archivo de la biblioteca",
  "flow2.merge.intro":
    "Elige qué se añade. Todo lo que sigue es opcional: lo que dejes vacío usa los ajustes del servidor.",
  "flow2.merge.subtitles": "Subtítulos que se añaden",
  "flow2.merge.subtitles.hint": "Reciben el mismo desfase que el audio.",
  "flow2.merge.subtitles.none": "El envío no trae subtítulos dentro.",
  "flow2.merge.tag": "Etiqueta de idioma",
  "flow2.merge.tag.hint": "Una etiqueta como es-419. Vacío: la del servidor.",
  "flow2.merge.tag.invalid":
    "Esa no es una etiqueta de idioma válida. Usa letras y guiones, como es-419 o en-US, con el idioma en minúsculas.",
  "flow2.merge.name": "Nombre de la pista",
  "flow2.merge.name.hint": "Cómo se llama la pista en los reproductores. Vacío: el del servidor.",
  "flow2.merge.keepDefault": "Mantener el audio original como pista predeterminada",
  "flow2.merge.confirm.title": "¿Fusionar el audio en el archivo de la biblioteca?",
  "flow2.merge.fact.audio": "Audio que se añade",
  "flow2.merge.fact.offset": "Desfase",
  "flow2.merge.fact.subtitles": "Subtítulos que se añaden",
  "flow2.merge.fact.subtitles.none": "Ninguno",
  "flow2.merge.fact.tag": "Etiqueta de idioma",
  "flow2.merge.fact.name": "Nombre de la pista",
  "flow2.merge.fact.default": "El del servidor",
  "flow2.merge.fact.file": "El archivo de la biblioteca",
  "flow2.merge.fact.file.value":
    "Radarr lo reemplaza por el archivo fusionado, que conserva todas sus pistas y tiene también las nuevas.",
  "flow2.merge.fact.title": "La película",
  "flow2.merge.fact.title.value":
    "Después queda sin monitorear, salvo que el servidor esté configurado de otro modo, para que las mejoras automáticas no reemplacen el archivo fusionado.",
  "flow2.merge.warning.replace":
    "El archivo anterior se elimina de la biblioteca. Si Radarr no tiene papelera de reciclaje, no se puede recuperar.",
  "flow2.merge.warning.search":
    "El botón de búsqueda de la propia película en Radarr aún puede reemplazar el archivo fusionado por una versión sin el audio nuevo.",
  "flow2.merge.submit": "Fusionar en el archivo de la biblioteca",
  "flow2.version.title": "Conservar como segunda versión",
  "flow2.version.intro":
    "Tu envío se guarda junto a la película, fuera de las carpetas de Radarr, y el servidor multimedia lo agrupa con ella.",
  "flow2.version.label": "Etiqueta de la versión",
  "flow2.version.label.hint":
    "Se muestra con la película en el servidor multimedia, por ejemplo Español. No uses palabras de calidad como 1080p o WEB-DL.",
  "flow2.version.name": "Nombre del archivo: {name}",
  "flow2.version.problem.title": "Esta etiqueta no se puede usar tal cual",
  "flow2.version.problem.label_empty":
    "No se pudo proponer ninguna etiqueta, porque el audio de tu envío no tiene etiqueta de idioma. Escribe una.",
  "flow2.version.problem.label_invalid":
    'La etiqueta no puede formar parte del nombre de un archivo. Quita las barras y caracteres como : * ? " < > |.',
  "flow2.version.problem.label_too_long": "La etiqueta es demasiado larga. Usa una más corta.",
  "flow2.version.problem.label_forbidden":
    "«{token}» es una palabra que no se admite en las etiquetas (palabras de calidad y de origen). Quítala.",
  "flow2.version.problem.label_extra":
    "El servidor multimedia toma por un extra, como un tráiler, un archivo que acaba en esta palabra, y no por una versión. Cambia la última palabra.",
  "flow2.version.problem.label_quality":
    "Radarr lee «{token}» en el nombre como una calidad. Quítala, o Radarr podría tomar el archivo por una copia mejor.",
  "flow2.version.problem.label_unchecked":
    "Radarr no pudo leer el nombre, así que no se pudo revisar la etiqueta. Prueba con una más sencilla.",
  "flow2.version.problem.unknown": "La etiqueta tiene un problema (código: {code}).",
  "flow2.version.problem.admin":
    "Administras este servidor: las palabras que no se admiten en las etiquetas son el ajuste versions.forbiddenLabelTokens, y la etiqueta propuesta sale de versions.labelTemplate.",
  "flow2.version.notices": "Conviene saber",
  "flow2.version.notice.outside_radarr":
    "Radarr no conoce esta versión. Nunca la renombra, la mejora ni la elimina, y solo reemplaza el archivo propio de la película.",
  "flow2.version.notice.regrouped_after_changes":
    "Cuando Radarr renombra o mejora el archivo de la película, el servidor multimedia pierde la agrupación. Fukku las agrupa otra vez solo, y mientras tanto aparecen como entradas separadas.",
  "flow2.version.notice.kept_on_delete":
    "Si eliminas la película en Radarr, la versión sigue en el disco. Quítala a mano.",
  "flow2.version.notice.subtitles_not_handed_over":
    "Los archivos de subtítulos de este envío no se entregan al gestor de subtítulos, que solo conoce el archivo propio de la película.",
  "flow2.version.notice.unknown": "Una nota del servidor (código: {code}).",
  "flow2.version.confirm.title": "¿Conservar esto como segunda versión?",
  "flow2.version.fact.label": "Etiqueta",
  "flow2.version.fact.name": "Nombre del archivo",
  "flow2.version.fact.where": "Dónde se guarda",
  "flow2.version.fact.where.value":
    "En la carpeta que el servidor reserva para versiones, no en la de Radarr. El archivo de la biblioteca no se toca.",
  "flow2.version.fact.server": "En el servidor multimedia",
  "flow2.version.fact.server.value":
    "Aparece con la película, como otra versión que se puede elegir.",
  "flow2.version.submit": "Conservar como segunda versión",
  "errors.not_flow2": "Este envío no es otro idioma de una película que esté en la biblioteca.",
  "errors.merge_not_offered":
    "Este envío no se puede fusionar en el archivo de la biblioteca. La comparación explica por qué.",
  "errors.no_audio": "El envío no tiene ninguna pista de audio para añadir.",
  "errors.preview_required":
    "Los clips de sincronización están desactualizados. Créalos otra vez con el desfase que quieras, revísalos y luego fusiona.",
  "errors.library_changed":
    "El archivo de la película en Radarr cambió desde que la elegiste. Elige la película otra vez para que Fukku compare el archivo nuevo.",
  "errors.version_label": "Esa etiqueta no se puede usar. Cámbiala e inténtalo de nuevo.",
  "errors.version_unavailable":
    "Ahora no es posible una segunda versión: está desactivada, una carpeta no se puede usar, o algunos archivos no pueden ser versiones. Pregunta a quien administra Fukku.",
  "errors.version_exists": "Ya existe una versión con este nombre. Elige otra etiqueta.",
  "errors.season_dub_choice": "Algunos episodios ya tienen un archivo. Elige qué hacer con ellos.",
  "errors.shared_file":
    "Reemplazar algunos de estos archivos también eliminaría el archivo de episodios que no están en este envío.",
  "jobError.library_unreadable":
    "Fukku no pudo leer el archivo de la película en la biblioteca. Avisa a quien administra el servidor.",
  "jobError.merge_failed":
    "No se pudo completar la fusión. La biblioteca no se cambió. Puedes volver a intentarlo.",
  "jobError.merge_rejected":
    "Radarr no aceptó el archivo fusionado. La biblioteca no se cambió. Avisa a quien administra el servidor.",
  "jobError.version_failed":
    "No se pudo guardar la segunda versión. La biblioteca no se cambió. Avisa a quien administra el servidor.",
  "import.merge.title": "La fusión",
  "import.merge.steps": "Pasos de la fusión",
  "import.merge.step.make": "Crear el archivo fusionado",
  "import.merge.step.import": "Radarr reemplaza su archivo",
  "import.merge.pending": "La fusión espera su turno.",
  "import.merge.running":
    "Se está fusionando el audio en una copia del archivo de la biblioteca. En una película grande tarda unos minutos. Esta página se actualiza sola.",
  "import.merge.merged": "El archivo fusionado está listo: {name} ({size}).",
  "import.merge.failed": "La fusión no terminó. El archivo de la biblioteca no se cambió.",
  "import.merge.refusal.library_unreadable": "No se pudo leer el archivo de la biblioteca.",
  "import.merge.refusal.upload_unreadable": "No se pudo leer el archivo subido.",
  "import.merge.refusal.track_not_found": "Una pista elegida no está en el envío.",
  "import.merge.refusal.free_space": "No había espacio libre suficiente para el archivo fusionado.",
  "import.merge.refusal.mux_failed": "La herramienta que une las pistas informó de un problema.",
  "import.merge.refusal.library_changed": "El archivo de la biblioteca cambió mientras se leía.",
  "import.merge.refusal.verify_failed":
    "El archivo fusionado no pasó las verificaciones (la imagen, la duración o las pistas no eran las esperadas).",
  "import.merge.refusal.busy":
    "Otra fusión estaba usando el mismo archivo de destino. Inténtalo de nuevo.",
  "import.merge.refusal.unknown": "Se rechazó la fusión (código: {code}).",
  "import.merge.fact.audio": "Pistas de audio",
  "import.merge.fact.subtitles": "Pistas de subtítulos",
  "import.merge.fact.offset": "Desfase",
  "import.merge.fact.tag": "Etiqueta de idioma",
  "import.merge.fact.name": "Nombre de la pista",
  "import.merge.fact.original": "El audio original sigue como pista predeterminada",
  "import.merge.fact.yes": "Sí",
  "import.merge.fact.no": "No",
  "import.merge.fact.offset.value": "{offset}\u00a0ms",
  "import.merge.done.title": "El archivo fusionado está en la biblioteca",
  "import.merge.done.body": "{app} reemplazó su archivo por {name}.",
  "import.merge.warning.search_can_replace.title": "Esta película no está monitoreada",
  "import.merge.warning.search_can_replace":
    "Las mejoras automáticas y las búsquedas automáticas de archivos que faltan no tocan el archivo fusionado. Pero el botón de búsqueda de la propia película en {app} aún puede reemplazarlo por una versión sin el audio nuevo, así que no lo uses si quieres conservar este.",
  "import.merge.warning.upgrade_can_replace.title": "Esta película sigue monitoreada",
  "import.merge.warning.upgrade_can_replace":
    "Una versión mejor sin el audio nuevo puede reemplazar el archivo fusionado. Para evitarlo, deja de monitorear la película en {app}.",
  "import.merge.warning.unknown.title": "El archivo fusionado aún puede reemplazarse",
  "import.merge.warning.unknown":
    "Algo en {app} aún puede reemplazar el archivo fusionado (código: {code}).",
  "import.unmonitored.merged":
    "Una fusión reemplazó el archivo, así que la película ya no está monitoreada: las mejoras automáticas no lo reemplazarán. El botón de búsqueda de la propia película en {app} sí puede hacerlo, así que no lo uses si quieres conservar este.",
  "import.version.running":
    "Fukku está guardando la versión junto a la película. Esta página se actualiza sola.",
  "import.version.done.title": "La segunda versión de {title} está guardada",
  "import.version.done.body": "Se conserva como {name}, con la etiqueta {label}.",
  "import.version.status.placing": "El archivo se está guardando.",
  "import.version.status.linked":
    "El archivo está en su lugar. Ahora lo agrupa el servidor multimedia.",
  "import.version.status.grouped": "El servidor multimedia la muestra con la película.",
  "import.version.status.waiting":
    "El servidor multimedia aún no la ha agrupado. Fukku lo intenta de nuevo solo.",
  "import.version.status.film_deleted":
    "Radarr ya no tiene esta película. La versión sigue en el disco.",
  "import.version.status.missing": "El archivo de la versión ya no está en el disco.",
  "import.version.status.unknown": "Estado de la versión: {status}.",
  "import.seasonDub.subtitlesOnly.title": "No se importó nada",
  "import.seasonDub.subtitlesOnly":
    "Elegiste añadir solo subtítulos para los episodios que ya tienen un archivo. Los subtítulos fueron al gestor de subtítulos, y los archivos siguen en este envío hasta que caduque.",
  "import.seasonDub.subtitlesOnly.mixed":
    "En los episodios que ya tenían un archivo elegiste añadir solo los subtítulos: esos archivos no se reemplazaron.",
  "import.seasonDub.replace.one":
    "Tus archivos reemplazaron a los que ya estaban en la biblioteca en la temporada {seasons}.",
  "import.seasonDub.replace.other":
    "Tus archivos reemplazaron a los que ya estaban en la biblioteca en las temporadas {seasons}.",
  "confirm.dub.title": "Episodios que ya tienen un archivo",
  "confirm.dub.intro":
    "Algunos episodios de este envío ya están en la biblioteca. Elige qué hacer con ellos. Los demás episodios se importan como siempre.",
  "confirm.dub.season.one":
    "Temporada {season}: ya hay archivos para {existing} de {count} episodio.",
  "confirm.dub.season.other":
    "Temporada {season}: ya hay archivos para {existing} de {count} episodios.",
  "confirm.dub.legend": "¿Qué debe pasar con ellos?",
  "confirm.dub.replace": "Reemplazar los archivos",
  "confirm.dub.replace.body":
    "Tus archivos se importan sobre los de la biblioteca, y la temporada queda sin monitorear para que las mejoras automáticas no los reemplacen. El botón de búsqueda de un episodio en {app} sí puede hacerlo.",
  "confirm.dub.subtitlesOnly": "Añadir solo los subtítulos",
  "confirm.dub.subtitlesOnly.body":
    "No se importa nada. Los subtítulos de tu envío van al gestor de subtítulos para los episodios que ya tienen un archivo.",
  "confirm.dub.unknown": "Otra opción ({policy})",
  "confirm.dub.unknown.body": "El servidor la ofrece, pero esta página todavía no la conoce.",
  "confirm.dub.decided": "Este servidor lo decide por sí mismo: {option}.",
  "confirm.dub.none":
    "En este servidor no se ofrece nada para estos episodios, así que se dejan fuera.",
  "confirm.dub.missing": "Elige qué hacer con los episodios que ya tienen un archivo.",
  "confirm.dub.subtitlesOnly.count":
    "No se importará nada. Los subtítulos van al gestor de subtítulos.",
  "confirm.dub.shared.title": "Reemplazar estos archivos quitaría los de otros episodios",
  "confirm.dub.shared.body":
    "Cada uno de estos archivos contiene varios episodios, y algunos no están en este envío. Reemplazar el archivo también quita esos episodios de la biblioteca.",
  "confirm.dub.shared.accept": "Reemplazar {name} de todos modos",
  "flow2.reason.media_server_too_old":
    "el servidor multimedia es un Jellyfin anterior a la 12.4, que no agrupa versiones de episodios.",
  "flow2.reason.media_server_unknown": "no se pudo leer la versión del servidor multimedia.",
  "flow2.reason.series_folder_unusable":
    "el nombre de la carpeta de la serie en Sonarr no puede formar parte del nombre de un archivo.",
  "flow2.reason.season_folder_unavailable":
    "una carpeta de temporada no puede recibir las versiones (no existe, no se puede escribir en ella o está en otro disco), o falta un archivo marcador necesario.",
  "confirm.dub.name.replace": "Reemplazar los archivos",
  "confirm.dub.name.subtitlesOnly": "Añadir solo los subtítulos",
  "confirm.dub.name.version": "Conservar como segundas versiones",
  "import.merge.warning.language_unknown_to_radarr.title": "Radarr no conoce el idioma nuevo",
  "import.merge.warning.language_unknown_to_radarr":
    "{app} no tiene un idioma para las pistas añadidas, así que muestra el archivo fusionado solo con los idiomas que ya tenía. Las pistas están etiquetadas en el archivo, pero los formatos personalizados y las búsquedas por idioma de {app} no las ven.",
  "confirm.dub.version": "Conservar como segundas versiones",
  "confirm.dub.version.body":
    "Cada uno de estos episodios conserva su propio archivo, y tu archivo se guarda junto a él como segunda versión. El servidor multimedia muestra ambos bajo el episodio. {app} no controla los segundos archivos.",
  "flow2.version.problem.label_episode":
    "La etiqueta no puede contener un número de temporada o de episodio (como S02E05, 2x05 o E03): el servidor multimedia leería el archivo como otro episodio.",
  "flow2.version.notice.untracked_by_sonarr":
    "Sonarr no controla los segundos archivos. Nunca los renombra ni los mejora, la mejora de un episodio reemplaza solo su propio archivo, y borrar la serie con sus archivos borra también las versiones.",
  "flow2.version.notice.adopted_without_episode_file":
    "Si se borra el archivo propio de un episodio, Sonarr toma el segundo archivo como el del episodio en su próximo escaneo.",
  "confirm.version.title": "Segundas versiones",
  "confirm.version.label": "Etiqueta de la versión",
  "confirm.version.label.hint":
    "Se muestra con cada episodio en el servidor multimedia, por ejemplo Español. No uses palabras de calidad como 1080p o WEB-DL, ni un número de temporada o de episodio.",
  "confirm.version.rows": "Episodios que se conservan como segundas versiones",
  "confirm.version.row.episode": "Temporada {season}, episodio {episode}",
  "confirm.version.row.waiting": "El nombre del archivo se fija con esta etiqueta al confirmar.",
  "confirm.version.row.double_episode":
    "Este archivo contiene varios episodios, así que no puede ser una segunda versión.",
  "confirm.version.row.shared_file":
    "El archivo del episodio en la biblioteca contiene también otros episodios, así que esto no puede ser una segunda versión.",
  "confirm.version.row.no_file": "Sonarr no tiene ningún archivo para este episodio.",
  "confirm.version.row.outside_library":
    "La carpeta del episodio está fuera de las carpetas de la biblioteca que Fukku puede usar.",
  "confirm.version.row.name_too_long":
    "El nombre del archivo sería demasiado largo. Usa una etiqueta más corta.",
  "confirm.version.row.version_exists":
    "Ya existe un archivo o una versión anterior con este nombre. Usa otra etiqueta.",
  "confirm.version.row.name_unreadable":
    "El nombre del archivo no se leería como exactamente este episodio. Pregunta a quien administra Fukku.",
  "confirm.version.row.folder_unreadable": "Fukku no puede leer la carpeta del episodio.",
  "confirm.version.row.unknown": "Este archivo no puede ser una segunda versión (código: {code}).",
  "confirm.version.count.one": "{count} episodio se conserva como segunda versión",
  "confirm.version.count.other": "{count} episodios se conservan como segundas versiones",
  "confirm.version.only.one":
    "No se importará nada nuevo. {count} episodio se conserva como segunda versión.",
  "confirm.version.only.other":
    "No se importará nada nuevo. {count} episodios se conservan como segundas versiones.",
  "confirm.version.refused.title": "Estos archivos no se pueden conservar como segundas versiones",
  "confirm.version.refused.leaveOut.hint.one":
    "Puedes dejar este archivo fuera. No tendrá segunda versión y su episodio conserva su propio archivo. Después elige Importar otra vez para confirmar el resto.",
  "confirm.version.refused.leaveOut.hint.other":
    "Puedes dejar estos archivos fuera. No tendrán segunda versión y sus episodios conservan sus propios archivos. Después elige Importar otra vez para confirmar el resto.",
  "confirm.version.leavingOut": "Dejando los archivos fuera…",
  "confirm.version.leftOut.done.one":
    "{count} archivo dejado fuera. Elige Importar para confirmar de nuevo.",
  "confirm.version.leftOut.done.other":
    "{count} archivos dejados fuera. Elige Importar para confirmar de nuevo.",
  "confirm.version.refused.leaveOut.one": "Dejar fuera {count} archivo",
  "confirm.version.refused.leaveOut.other": "Dejar fuera {count} archivos",
  "confirm.state.version": "Segunda versión",
  "confirm.version.kept":
    "Este episodio ya tiene un archivo. Tu archivo se conserva a su lado como segunda versión.",
  "confirm.version.leaveOut": "Dejar fuera de las segundas versiones",
  "confirm.version.takeBack": "Conservar de nuevo como segunda versión",
  "confirm.version.leftOut":
    "No se crea una segunda versión para este episodio. Su archivo se queda como está.",
  "confirm.version.refused.hint.fixed":
    "Este servidor lo decide por sí mismo. Cambia la etiqueta, o vuelve atrás y elige otro título.",
  "jobError.version_upgrade":
    "Sonarr tomaría todas las segundas versiones por los archivos propios de los episodios, así que no se guardó ninguna. La biblioteca no se cambió.",
  "jobError.version_unchecked":
    "Sonarr no dio una respuesta clara sobre ninguna segunda versión, así que no se guardó ninguna. La biblioteca no se cambió.",
  "jobError.version_exists":
    "Todas las segundas versiones ya existen con su nombre, así que no se guardó ninguna. La biblioteca no se cambió.",
  "import.episodeVersion.partial.title": "{placed} de {total} segundas versiones están en su lugar",
  "import.episodeVersion.partial.body.one":
    "{count} no se pudo guardar. Mira cada versión más abajo.",
  "import.episodeVersion.partial.body.other":
    "{count} no se pudieron guardar. Mira cada versión más abajo.",
  "confirm.version.label.proposed": "Si esto queda vacío, el servidor usa «{label}».",
  "confirm.version.refused.hint":
    "Elige otra opción arriba, cambia la etiqueta, o vuelve atrás y elige otro título.",
  "confirm.version.refusal.version_upgrade":
    "Sonarr tomaría este archivo por una mejora del archivo propio del episodio en su próximo escaneo.",
  "confirm.version.refusal.version_unchecked":
    "Sonarr no da una respuesta clara sobre este archivo: no lo ofrece, o lo lee como otro episodio.",
  "confirm.version.refusal.version_unavailable": "Este archivo no puede ser una segunda versión.",
  "confirm.version.refusal.version_exists":
    "Ya existe un archivo o una versión anterior con este nombre.",
  "import.row.version": "Segunda versión",
  "import.step.mediaServerEpisodeVersions":
    "Agrupar las segundas versiones con los episodios en el servidor multimedia",
  "import.step.mediaServerEpisodeVersions.failed":
    "El servidor multimedia no agrupó las versiones con los episodios. Fukku lo intenta de nuevo solo; mientras tanto aparecen como entradas separadas.",
  "import.episodeVersion.title": "Segundas versiones",
  "import.episodeVersion.label": "Etiqueta: {label}",
  "import.episodeVersion.item": "S{season}E{episode}",
  "import.episodeVersion.done.title": "Las segundas versiones de {title} están en su lugar",
  "import.episodeVersion.done.body.one":
    "Se conserva {count} archivo junto al archivo propio de su episodio.",
  "import.episodeVersion.done.body.other":
    "Se conservan {count} archivos junto a los archivos propios de sus episodios.",
  "import.episodeVersion.status.placing": "El archivo se está guardando.",
  "import.episodeVersion.status.linked":
    "El archivo está en su lugar. Ahora lo agrupa el servidor multimedia.",
  "import.episodeVersion.status.grouped": "El servidor multimedia la muestra con el episodio.",
  "import.episodeVersion.status.waiting":
    "El servidor multimedia aún no la ha agrupado. Fukku lo intenta de nuevo solo.",
  "import.episodeVersion.status.missing": "El archivo de la versión ya no está en el disco.",
  "import.episodeVersion.status.refused": "No se guardó.",
  "import.episodeVersion.status.adopted":
    "Sonarr ahora controla este archivo como el propio del episodio, así que puede renombrarlo, mejorarlo o borrarlo.",
  "import.episodeVersion.status.adoptable":
    "Sonarr tomaría este archivo como el propio del episodio en su próximo escaneo.",
  "import.episodeVersion.status.episode_file_missing":
    "El archivo propio del episodio ya no está, así que Sonarr toma este en su lugar en su próximo escaneo.",
  "import.episodeVersion.status.series_moved":
    "Sonarr movió la serie sin este archivo. La versión se queda donde estaba.",
  "import.episodeVersion.status.series_deleted":
    "Sonarr ya no tiene esta serie. La versión sigue en el disco; bórrala a mano.",
  "import.episodeVersion.status.unknown": "Estado de la versión: {status}.",
  "import.episodeVersion.refusal.version_upgrade":
    "Sonarr tomaría este archivo como el propio del episodio, así que no se guardó.",
  "import.episodeVersion.refusal.version_unchecked":
    "Sonarr no dio una respuesta clara sobre este archivo, así que no se guardó.",
  "import.episodeVersion.refusal.version_exists":
    "Ya existe un archivo con este nombre, así que no se reemplazó nada.",
  "import.episodeVersion.refusal.version_failed":
    "No se pudo guardar. Avisa a quien administra el servidor.",
  "import.episodeVersion.refusal.unknown": "No se guardó (código: {code}).",
  "errors.version_upgrade":
    "Sonarr tomaría algunos de estos archivos por los archivos propios de los episodios, así que no se pueden conservar como segundas versiones.",
  "errors.version_unchecked":
    "Sonarr no da una respuesta clara sobre algunos de estos archivos, así que no se pueden conservar como segundas versiones.",
};

export default es;
