package com.nocap.link.notes

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.staggeredgrid.LazyVerticalStaggeredGrid
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridCells
import androidx.compose.foundation.lazy.staggeredgrid.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withLink
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.ui.NcDialog
import com.nocap.link.ui.NcIcon
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.NoCapTheme
import com.nocap.link.ui.PillButton
import com.nocap.link.ui.SearchField
import java.text.DateFormat
import java.util.Calendar
import java.util.Date
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Sticky Notes on the phone ("Brain Dump" in Gen Z): the same notes as on
 * NoCapOS, through this phone's link. Opened from NoCap Link, the widget and
 * reminder notifications.
 */
class NotesActivity : ComponentActivity() {
    /** A note to open (from the widget or a reminder). */
    var openId by mutableStateOf<String?>(null)
    var resumeTick by mutableStateOf(0)

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        Prefs.init(this)
        NotesStore.init(this)
        openId = intent?.getStringExtra(EXTRA_NOTE)
        setContent { NoCapTheme { NotesScreen(this) } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        intent.getStringExtra(EXTRA_NOTE)?.let { openId = it }
    }

    override fun onResume() {
        super.onResume()
        resumeTick++
    }

    companion object {
        const val EXTRA_NOTE = "note"
    }
}

private fun noteTint(color: String) = Color(NoteDoc.tint(color))

/** A note's glass: the card colour washed with its tint. */
private fun noteBg(color: String): Color =
    if (color == "glass") NoCap.Card else if (color == "ink") Color(0xF0101116) else lerp(NoCap.GlassStrong, noteTint(color), if (NoCap.light) 0.18f else 0.24f)

private fun noteInk(color: String): Color = if (color == "ink") Color(0xFFEEF0F4) else NoCap.Text

@Composable
fun NotesScreen(a: NotesActivity) {
    val scope = rememberCoroutineScope()
    var view by remember { mutableStateOf(NoteDoc.View.NOTES) }
    var query by remember { mutableStateOf("") }
    var trashPick by remember { mutableStateOf<Note?>(null) }
    var askEmpty by remember { mutableStateOf(false) }
    LaunchedEffect(a.resumeTick) { NotesStore.refresh(a) }

    val editing = a.openId?.let { id -> NotesStore.notes.firstOrNull { it.id == id } }
    if (editing != null) {
        NoteEditorScreen(a, editing) { a.openId = null }
        return
    }
    BackHandler { a.finish() }

    val list = NoteDoc.visible(NotesStore.notes, view, query)
    Column(Modifier.fillMaxSize().background(NoCap.Sheet).statusBarsPadding().navigationBarsPadding(), horizontalAlignment = Alignment.CenterHorizontally) {
        Row(Modifier.widthIn(max = 900.dp).fillMaxWidth().padding(start = 8.dp, end = 12.dp, top = 8.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(44.dp).clip(CircleShape).clickable { a.finish() }, contentAlignment = Alignment.Center) {
                NcIcon("chevronLeft", NoCap.Text, 22.dp, desc = t("Back", "Back"))
            }
            Text("NoCap Notes", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium.copy(fontSize = 22.sp))
            if (view == NoteDoc.View.NOTES) {
                Box(
                    Modifier.size(44.dp).clip(CircleShape).background(NoCap.Red).clickable {
                        scope.launch { NotesStore.create(a)?.let { a.openId = it.id } }
                    },
                    contentAlignment = Alignment.Center,
                ) { NcIcon("plus", Color.White, 22.dp, desc = t("New note", "New note")) }
            }
        }
        Column(Modifier.widthIn(max = 900.dp).fillMaxWidth().padding(horizontal = 14.dp)) {
            SearchField(query, { query = it }, t("Search your brain", "Search notes"))
            Spacer(Modifier.size(10.dp))
            Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(50)).background(NoCap.Field).padding(3.dp)) {
                for ((v, label) in listOf(
                    NoteDoc.View.NOTES to t("Notes", "Notes"),
                    NoteDoc.View.ARCHIVE to t("The vault", "Archive"),
                    NoteDoc.View.TRASH to t("Trash", "Trash"),
                )) {
                    val on = v == view
                    Box(
                        Modifier.weight(1f).clip(RoundedCornerShape(50)).background(if (on) NoCap.GlassStrong else Color.Transparent)
                            .clickable { view = v }.padding(vertical = 9.dp),
                        contentAlignment = Alignment.Center,
                    ) { Text(label, style = MaterialTheme.typography.labelLarge, color = if (on) NoCap.Text else NoCap.Muted) }
                }
            }
            if (NotesStore.problem == "old") Hint(t("Your server needs a NoCapOS update for notes.", "Your server needs a NoCapOS update for Sticky Notes."))
            else if (NotesStore.problem == "unlinked") Hint(t("Link this phone to your server first.", "Link this phone to your server first."))
            else if (NotesStore.problem.isNotEmpty() && NotesStore.notes.isNotEmpty()) Hint(t("Offline rn, showing what's saved here.", "Offline: showing the notes saved on this phone."))
            if (view == NoteDoc.View.TRASH && list.isNotEmpty()) {
                Row(Modifier.fillMaxWidth().padding(top = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(t("Trash gets yeeted for good after 30 days.", "Notes in the trash are deleted after 30 days."), Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
                    PillButton(t("Empty trash", "Empty trash"), { askEmpty = true }, primary = false)
                }
            }
        }
        Spacer(Modifier.size(10.dp))
        if (list.isEmpty()) {
            Column(Modifier.weight(1f).padding(32.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                NcIcon("stickyNote", NoCap.Red, 44.dp)
                Spacer(Modifier.size(12.dp))
                Text(
                    when {
                        NotesStore.loading && NotesStore.notes.isEmpty() -> t("Loading your thoughts…", "Loading notes…")
                        query.isNotBlank() -> t("Nothing matches. Try other words?", "No notes match your search.")
                        view == NoteDoc.View.ARCHIVE -> t("The vault's empty", "Nothing archived")
                        view == NoteDoc.View.TRASH -> t("Trash is clean", "Trash is empty")
                        else -> t("Brain's empty rn", "No notes yet")
                    },
                    style = MaterialTheme.typography.titleMedium,
                )
                if (view == NoteDoc.View.NOTES && query.isBlank()) {
                    Text(
                        t("Dump ideas and checklists. They sync with your NoCap desktop.", "Write notes and checklists. They sync with your NoCapOS desktop."),
                        Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
                    )
                }
            }
        } else {
            LazyVerticalStaggeredGrid(
                StaggeredGridCells.Adaptive(160.dp),
                Modifier.widthIn(max = 900.dp).fillMaxWidth().weight(1f),
                contentPadding = androidx.compose.foundation.layout.PaddingValues(start = 14.dp, end = 14.dp, bottom = 24.dp),
                horizontalArrangement = Arrangement.spacedBy(10.dp),
                verticalItemSpacing = 10.dp,
            ) {
                items(list, key = { it.id }) { n ->
                    NoteCard(n, onTick = { i -> scope.launch { NotesStore.change(a, n.id, NoteChange(body = NoteDoc.toggle(n.body, i))) } }) {
                        if (n.trashed) trashPick = n else a.openId = n.id
                    }
                }
            }
        }
    }

    trashPick?.let { n ->
        NcDialog(
            NoteDoc.title(n).ifEmpty { t("Untitled note", "Untitled note") }, t("In the trash. Bring it back or delete it for good?", "This note is in the trash."),
            onDismiss = { trashPick = null },
            confirm = t("Bring it back", "Restore") to { trashPick = null; scope.launch { NotesStore.change(a, n.id, NoteChange(trashed = false)) } },
            dismiss = t("Delete forever", "Delete forever") to { trashPick = null; scope.launch { NotesStore.deleteForever(a, n.id) } },
        )
    }
    if (askEmpty) {
        NcDialog(
            t("Empty the trash?", "Empty the trash?"), t("Those notes are gone forever. No undo, fr.", "The notes in the trash will be deleted for good. This can't be undone."),
            onDismiss = { askEmpty = false }, danger = true,
            confirm = t("Empty trash", "Empty trash") to { askEmpty = false; scope.launch { NotesStore.emptyTrash(a) } },
            dismiss = t("Nah", "Cancel") to { askEmpty = false },
        )
    }
}

@Composable
private fun Hint(text: String) {
    Text(text, Modifier.padding(top = 8.dp, start = 4.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
}

@Composable
private fun NoteCard(n: Note, onTick: (Int) -> Unit, onOpen: () -> Unit) {
    val ink = noteInk(n.color)
    val shape = RoundedCornerShape(18.dp)
    Column(
        Modifier.fillMaxWidth().clip(shape).background(noteBg(n.color))
            .border(1.dp, if (n.color == "glass") NoCap.GlassEdge else noteTint(n.color).copy(alpha = 0.45f), shape)
            .clickable(onClick = onOpen).padding(14.dp),
    ) {
        val title = NoteDoc.title(n)
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                title.ifEmpty { t("Untitled note", "Untitled note") }, Modifier.weight(1f),
                style = MaterialTheme.typography.titleSmall.copy(fontWeight = FontWeight.Bold), color = if (title.isEmpty()) ink.copy(alpha = 0.5f) else ink,
                maxLines = 2, overflow = TextOverflow.Ellipsis,
            )
            if (n.pinned && !n.trashed) NcIcon("pin", NoCap.Red, 15.dp)
        }
        val body = if (n.title.isBlank()) n.body.drop(1) else n.body
        if (body.any { it.text.isNotBlank() }) {
            Spacer(Modifier.size(6.dp))
            Lines(body.take(10), ink, if (n.trashed) null else { i -> onTick(if (n.title.isBlank()) i + 1 else i) })
        }
        val (done, total) = NoteDoc.progress(n.body)
        if (total > 0 || n.remindAt != null || n.onDesk) {
            Spacer(Modifier.size(8.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                if (total > 0) Tag("checklist", "$done/$total", ink)
                n.remindAt?.let { Tag("bell", whenLabel(it), ink, struck = n.reminded) }
                if (n.onDesk) Tag("desktop", "", ink)
            }
        }
    }
}

@Composable
private fun Tag(icon: String, text: String, ink: Color, struck: Boolean = false) {
    Row(
        Modifier.clip(RoundedCornerShape(50)).background(ink.copy(alpha = 0.09f)).padding(horizontal = 8.dp, vertical = 3.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        NcIcon(icon, ink.copy(alpha = 0.8f), 12.dp)
        if (text.isNotEmpty()) {
            Text(
                text, Modifier.padding(start = 4.dp), style = MaterialTheme.typography.labelSmall.copy(textDecoration = if (struck) TextDecoration.LineThrough else null),
                color = ink.copy(alpha = 0.8f), maxLines = 1,
            )
        }
    }
}

/** Note lines drawn with their marks; checklist boxes tick when [onTick] is set. */
@Composable
private fun Lines(blocks: List<NoteBlock>, ink: Color, onTick: ((Int) -> Unit)?) {
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        blocks.forEachIndexed { i, b ->
            Row(verticalAlignment = Alignment.Top) {
                when (b.type) {
                    NoteBlock.TODO -> {
                        Box(
                            Modifier.padding(top = 2.dp, end = 8.dp).size(18.dp).clip(RoundedCornerShape(5.dp))
                                .background(if (b.done) NoCap.Red else Color.Transparent)
                                .border(1.5.dp, if (b.done) NoCap.Red else ink.copy(alpha = 0.45f), RoundedCornerShape(5.dp))
                                .let { m -> if (onTick != null) m.clickable { onTick(i) } else m },
                            contentAlignment = Alignment.Center,
                        ) { if (b.done) NcIcon("check", Color.White, 13.dp) }
                    }
                    NoteBlock.LI -> Box(Modifier.padding(top = 8.dp, start = 4.dp, end = 10.dp).size(5.dp).clip(CircleShape).background(ink.copy(alpha = 0.7f)))
                }
                Text(
                    rich(b),
                    style = MaterialTheme.typography.bodyMedium.copy(textDecoration = if (b.done) TextDecoration.LineThrough else null),
                    color = if (b.done) ink.copy(alpha = 0.55f) else ink,
                )
            }
        }
    }
}

private fun rich(b: NoteBlock): AnnotatedString = buildAnnotatedString {
    for (s in b.spans) {
        val style = SpanStyle(fontWeight = if (s.bold) FontWeight.Bold else null, fontStyle = if (s.italic) FontStyle.Italic else null)
        val href = s.href?.takeIf { it.startsWith("https://") || it.startsWith("http://") || it.startsWith("mailto:") }
        if (href != null) {
            withLink(LinkAnnotation.Url(href, TextLinkStyles(SpanStyle(textDecoration = TextDecoration.Underline)))) { withStyle(style) { append(s.text) } }
        } else withStyle(style) { append(s.text) }
    }
}

/** "Today 14:30", "Tomorrow 09:00", else the date. */
private fun whenLabel(ms: Long): String {
    val c = Calendar.getInstance().apply { timeInMillis = ms }
    val now = Calendar.getInstance()
    val days = (c.get(Calendar.YEAR) - now.get(Calendar.YEAR)) * 366 + c.get(Calendar.DAY_OF_YEAR) - now.get(Calendar.DAY_OF_YEAR)
    val time = DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(ms))
    return when (days) {
        0 -> t("Today", "Today") + " " + time
        1 -> t("Tomorrow", "Tomorrow") + " " + time
        else -> DateFormat.getDateInstance(DateFormat.SHORT).format(Date(ms)) + " " + time
    }
}

/** One note, edited as plain lines; saved as you type and when you leave. */
@Composable
private fun NoteEditorScreen(a: NotesActivity, n: Note, close: () -> Unit) {
    val scope = rememberCoroutineScope()
    var title by remember(n.id) { mutableStateOf(n.title) }
    var text by remember(n.id) { mutableStateOf(NoteDoc.plain(n.body)) }
    var editing by remember(n.id) { mutableStateOf(NoteDoc.isEmpty(n)) }
    var saved by remember(n.id) { mutableStateOf(n.title to NoteDoc.plain(n.body)) }
    var remind by remember { mutableStateOf(false) }
    var colors by remember { mutableStateOf(false) }

    fun save() {
        if (saved == title to text) return
        val body = NoteDoc.fromPlain(text, n.body)
        saved = title to text
        scope.launch { NotesStore.change(a, n.id, NoteChange(title = title.trim(), body = body)) }
    }
    fun leave() {
        save()
        // A new note left empty isn't kept.
        if (title.isBlank() && text.isBlank() && n.remindAt == null && !n.pinned) scope.launch { NotesStore.deleteForever(a, n.id) }
        close()
    }
    LaunchedEffect(title, text) {
        delay(800)
        save()
    }
    // Changed elsewhere while open and not being typed in here: follow it.
    LaunchedEffect(n.version) {
        if (!editing && saved == title to text) {
            title = n.title
            text = NoteDoc.plain(n.body)
            saved = title to text
        }
    }
    BackHandler { if (editing && !NoteDoc.isEmpty(n)) { save(); editing = false } else leave() }

    val ink = noteInk(n.color)
    Column(Modifier.fillMaxSize().background(NoCap.Sheet).background(noteBg(n.color)).statusBarsPadding().navigationBarsPadding().imePadding()) {
        Row(Modifier.fillMaxWidth().padding(start = 8.dp, end = 8.dp, top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(44.dp).clip(CircleShape).clickable { leave() }, contentAlignment = Alignment.Center) {
                NcIcon("chevronLeft", ink, 22.dp, desc = t("Back", "Back"))
            }
            Spacer(Modifier.weight(1f))
            ToolButton(if (n.pinned) "pin" else "pin", if (n.pinned) t("Unpin", "Unpin") else t("Pin it", "Pin"), on = n.pinned, ink = ink) {
                scope.launch { NotesStore.change(a, n.id, NoteChange(pinned = !n.pinned)) }
            }
            ToolButton("palette", t("Color", "Color"), ink = ink) { colors = !colors }
            ToolButton("bell", t("Remind me", "Reminder"), on = n.remindAt != null && !n.reminded, ink = ink) { remind = true }
            ToolButton("archiveBox", if (n.archived) t("Take out of the vault", "Unarchive") else t("Send to the vault", "Archive"), ink = ink) {
                save(); scope.launch { NotesStore.change(a, n.id, NoteChange(archived = !n.archived)) }; close()
            }
            ToolButton("trash", t("Toss it", "Move to trash"), ink = ink) {
                save(); scope.launch { NotesStore.change(a, n.id, NoteChange(trashed = true)) }; close()
            }
        }
        if (colors) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 18.dp, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                for (c in NoteDoc.COLORS) {
                    Box(
                        Modifier.size(30.dp).clip(CircleShape).background(if (c == "glass") NoCap.GlassStrong else noteTint(c))
                            .border(if (c == n.color) 3.dp else 1.dp, if (c == n.color) NoCap.Red else NoCap.GlassEdge, CircleShape)
                            .clickable { colors = false; scope.launch { NotesStore.change(a, n.id, NoteChange(color = c)) } },
                    )
                }
            }
        }
        Column(Modifier.weight(1f).fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp)) {
            Box {
                if (title.isEmpty()) Text(t("Title", "Title"), style = MaterialTheme.typography.headlineSmall, color = ink.copy(alpha = 0.4f))
                BasicTextField(
                    title, { title = it.replace("\n", " ").take(300) }, Modifier.fillMaxWidth(), singleLine = true,
                    textStyle = MaterialTheme.typography.headlineSmall.copy(color = ink, fontWeight = FontWeight.Bold), cursorBrush = SolidColor(NoCap.Red),
                )
            }
            Spacer(Modifier.size(12.dp))
            if (editing) {
                Box(Modifier.fillMaxWidth().heightIn(min = 240.dp)) {
                    if (text.isEmpty()) {
                        Text(t("Spill it… Start a line with - for bullets or [ ] for a checklist.", "Start typing… Begin a line with - for a bullet or [ ] for a checklist."), style = MaterialTheme.typography.bodyLarge, color = ink.copy(alpha = 0.4f))
                    }
                    BasicTextField(
                        text, { text = it }, Modifier.fillMaxWidth().heightIn(min = 240.dp),
                        textStyle = MaterialTheme.typography.bodyLarge.copy(color = ink), cursorBrush = SolidColor(NoCap.Red),
                    )
                }
            } else {
                Box(Modifier.fillMaxWidth().heightIn(min = 240.dp).clickable { editing = true }) {
                    Lines(n.body, ink) { i -> scope.launch { NotesStore.change(a, n.id, NoteChange(body = NoteDoc.toggle(n.body, i))) } }
                }
            }
        }
        Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            n.remindAt?.let { Tag("bell", whenLabel(it), ink, struck = n.reminded); Spacer(Modifier.width(8.dp)) }
            Spacer(Modifier.weight(1f))
            Text(
                if (saved != title to text) t("Saving…", "Saving…") else t("Saved, bestie", "Saved"),
                style = MaterialTheme.typography.labelMedium, color = ink.copy(alpha = 0.6f),
            )
            if (!editing) {
                Spacer(Modifier.width(12.dp))
                PillButton(t("Edit", "Edit"), { editing = true }, primary = false, icon = "edit")
            }
        }
    }

    if (remind) {
        val presets = remember { reminderPresets() }
        NcDialog(
            t("Remind me", "Reminder"), t("Your phone and your NoCap desktop both get the ping.", "You'll be notified on this phone and on your NoCapOS desktop."),
            onDismiss = { remind = false },
            dismiss = if (n.remindAt != null) t("Drop the reminder", "Remove reminder") to { remind = false; scope.launch { NotesStore.change(a, n.id, NoteChange(remindAt = 0)) } } else null,
            extra = presets.map { (label, at) -> label to { remind = false; scope.launch { NotesStore.change(a, n.id, NoteChange(remindAt = at)) }; Unit } },
        )
    }
}

@Composable
private fun ToolButton(icon: String, desc: String, on: Boolean = false, ink: Color, onClick: () -> Unit) {
    Box(
        Modifier.size(42.dp).clip(CircleShape).background(if (on) NoCap.Red.copy(alpha = 0.16f) else Color.Transparent).clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { NcIcon(icon, if (on) NoCap.Red else ink.copy(alpha = 0.85f), 20.dp, desc = desc) }
}

/** In an hour, tonight (when it's still ahead), tomorrow morning, next Monday morning. */
private fun reminderPresets(now: Calendar = Calendar.getInstance()): List<Pair<String, Long>> {
    val out = ArrayList<Pair<String, Long>>()
    out += t("In an hour", "In 1 hour") to (now.timeInMillis + 60 * 60_000L) / 60_000L * 60_000L
    val tonight = (now.clone() as Calendar).apply { set(Calendar.HOUR_OF_DAY, 20); set(Calendar.MINUTE, 0); set(Calendar.SECOND, 0); set(Calendar.MILLISECOND, 0) }
    if (tonight.timeInMillis > now.timeInMillis + 15 * 60_000L) out += t("Tonight", "Tonight") to tonight.timeInMillis
    val tomorrow = (now.clone() as Calendar).apply { add(Calendar.DAY_OF_YEAR, 1); set(Calendar.HOUR_OF_DAY, 9); set(Calendar.MINUTE, 0); set(Calendar.SECOND, 0); set(Calendar.MILLISECOND, 0) }
    out += t("Tomorrow morning", "Tomorrow morning") to tomorrow.timeInMillis
    val monday = (now.clone() as Calendar).apply {
        do add(Calendar.DAY_OF_YEAR, 1) while (get(Calendar.DAY_OF_WEEK) != Calendar.MONDAY)
        set(Calendar.HOUR_OF_DAY, 9); set(Calendar.MINUTE, 0); set(Calendar.SECOND, 0); set(Calendar.MILLISECOND, 0)
    }
    out += t("Next week", "Next week") to monday.timeInMillis
    return out
}
