package com.nocap.link.ai

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.remember
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.sp
import com.nocap.link.ui.GlassLevel
import com.nocap.link.ui.NcIcon
import com.nocap.link.ui.PillButton
import com.nocap.link.ui.Shell
import com.nocap.link.ui.ShellIconButton
import com.nocap.link.ui.shellGlass
import android.content.Intent
import android.os.Bundle
import android.speech.RecognizerIntent
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.Icons
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.lifecycleScope
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.data.Vault
import com.nocap.link.MainActivity
import com.nocap.link.net.ChatMessage
import com.nocap.link.net.Conversation
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.ServerTooOld
import com.nocap.link.ui.glass
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.NoCapTheme
import com.nocap.link.ui.RedButton
import com.nocap.link.ui.Wallpaper
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * NoCap AI: chats with the AI Assistant on your NoCapOS server, as you,
 * with your own conversations and memory. Replies stream in.
 */
class AiActivity : ComponentActivity() {
    private var api: NoCapApi? = null
    private var conversations by mutableStateOf<List<Conversation>>(emptyList())
    private var current by mutableStateOf<String?>(null)
    private val messages = mutableStateListOf<ChatMessage>()
    private var input by mutableStateOf("")
    private var thinking by mutableStateOf(false)
    private var error by mutableStateOf<String?>(null)
    private var showList by mutableStateOf(false)
    private var serverVersion: String? = null
    private var sendJob: Job? = null
    private var openJob: Job? = null
    /** Bumped when the shown chat changes, so a reply still streaming for another chat doesn't land here. */
    private var shown = 0
    /** Looked up once (not on every keystroke). */
    private val canListen by lazy { runCatching { packageManager.resolveActivity(Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH), 0) != null }.getOrDefault(false) }

    private val listen = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val said = r.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
        if (!said.isNullOrBlank()) input = if (input.isBlank()) said else "$input $said"
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        Prefs.init(this)
        // A broken keystore or a bad saved link shows the "link first" screen instead of crashing.
        api = runCatching { Vault.get(this).link?.let { NoCapApi(it) } }.getOrNull()
        val prompt = intent.getStringExtra(EXTRA_PROMPT)
        // Reopened: the chat you had open, as you left it (then refreshed from the server).
        val restored = prompt == null && Kept.current != null
        if (restored) {
            current = Kept.current
            messages.addAll(Kept.messages)
            input = Kept.input
        }
        prompt?.let { input = it }
        setContent { NoCapTheme { Screen() } }
        if (api != null) {
            loadList(pickLatest = prompt == null && !restored)
            if (restored) Kept.current?.let { refresh(it) }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // Already open (singleTop): a new prompt just lands in the input box.
        intent.getStringExtra(EXTRA_PROMPT)?.let { input = it }
    }

    override fun onPause() {
        super.onPause()
        Kept.current = current
        Kept.input = input
        Kept.messages = messages.filter { it.content.isNotEmpty() }.toList()
    }

    /** Reloads the open chat without blanking it first (no flash on reopen). */
    private fun refresh(id: String) {
        val api = api ?: return
        val mine = shown
        lifecycleScope.launch {
            val list = runCatching { withContext(Dispatchers.IO) { api.conversation(id) } }.getOrNull() ?: return@launch
            if (mine == shown && current == id && !thinking) { messages.clear(); messages.addAll(list) }
        }
    }

    private fun fail(e: Exception) {
        error = if (e is ServerTooOld) MainActivity.needsUpdate(serverVersion) else MainActivity.friendly(e)
    }

    private fun loadList(pickLatest: Boolean) {
        val api = api ?: return
        lifecycleScope.launch {
            try {
                serverVersion = runCatching { withContext(Dispatchers.IO) { api.pairInfo().version } }.getOrNull()
                conversations = withContext(Dispatchers.IO) { api.conversations() }
                if (pickLatest && current == null) conversations.firstOrNull()?.let { open(it.id) }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                fail(e)
            }
        }
    }

    private fun open(id: String) {
        val api = api ?: return
        current = id
        showList = false
        messages.clear()
        error = null
        val mine = ++shown
        openJob?.cancel()
        openJob = lifecycleScope.launch {
            try {
                val list = withContext(Dispatchers.IO) { api.conversation(id) }
                if (mine == shown) messages.addAll(list)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                fail(e)
            }
        }
    }

    private fun newChat() {
        shown++
        openJob?.cancel()
        current = null
        messages.clear()
        showList = false
        error = null
    }

    private fun delete(id: String) {
        val api = api ?: return
        lifecycleScope.launch {
            try {
                withContext(Dispatchers.IO) { api.deleteConversation(id) }
                if (current == id) newChat()
                conversations = conversations.filter { it.id != id }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                fail(e)
            }
        }
    }

    private fun send() {
        val api = api ?: return
        val text = input.trim()
        if (text.isEmpty() || thinking) return
        input = ""
        error = null
        thinking = true
        messages.add(ChatMessage("user", text))
        messages.add(ChatMessage("assistant", ""))
        val mine = shown
        sendJob = lifecycleScope.launch {
            try {
                val id = current ?: withContext(Dispatchers.IO) { api.newConversation().id }.also { if (mine == shown) current = it }
                withContext(Dispatchers.IO) {
                    api.chat(id, text) { delta ->
                        runOnUiThread {
                            // Only into the chat it belongs to (you may have switched chats meanwhile).
                            val last = messages.lastIndex
                            if (mine == shown && last >= 0 && messages[last].role == "assistant") messages[last] = messages[last].copy(content = messages[last].content + delta)
                        }
                    }
                }
                val fresh = withContext(Dispatchers.IO) { runCatching { api.conversations() }.getOrNull() }
                if (fresh != null) conversations = fresh
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                if (mine == shown) {
                    fail(e)
                    if (messages.lastOrNull()?.let { it.role == "assistant" && it.content.isEmpty() } == true) messages.removeAt(messages.lastIndex)
                }
            } finally {
                thinking = false
            }
        }
    }

    private fun voice() {
        val i = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH)
            .putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
            .putExtra(RecognizerIntent.EXTRA_PROMPT, t("Spill it…", "Speak now"))
        try { listen.launch(i) } catch (_: Exception) { error = t("No voice typing on this phone.", "Speech recognition isn't available on this phone.") }
    }

    private fun voiceAvailable() = canListen

    /*
     * Laid out like the web OS AI Assistant: a top bar (chats, title, new
     * chat), the thread with the assistant's avatar and accent user bubbles,
     * the welcome orb with suggestions, and the compose box at the bottom;
     * the chat list slides in from the left as a drawer.
     */
    @Composable
    private fun Screen() {
        BackHandler(showList) { showList = false }
        Wallpaper {
            Box(Modifier.fillMaxSize().background(Shell.tint(GlassLevel.SHEET)))
            // Clear of status/nav bars, cutouts and the keyboard, so nothing sits under them.
            Box(Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing)) {
                Column(Modifier.fillMaxSize()) {
                    TopBar()
                    if (api == null) {
                        Column(Modifier.weight(1f).fillMaxWidth().padding(24.dp), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                            Orb(68.dp)
                            Spacer(Modifier.height(14.dp))
                            Text(t("NoCap AI lives on your NoCap server. Link this phone first.", "NoCap AI runs on your NoCap server. Link this phone first."), style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center)
                            Spacer(Modifier.height(16.dp))
                            PillButton(t("Link with NoCap Link", "Link with NoCap Link"), {
                                startActivity(Intent(this@AiActivity, MainActivity::class.java).setAction(MainActivity.ACTION_HUB))
                                finish()
                            })
                        }
                        return@Column
                    }
                    Box(Modifier.weight(1f).fillMaxWidth()) { Chat() }
                    error?.let { e ->
                        Row(
                            Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp).clip(RoundedCornerShape(12.dp)).background(NoCap.Bad.copy(alpha = 0.1f)).padding(horizontal = 12.dp, vertical = 9.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            NcIcon("alert", NoCap.Bad, 16.dp)
                            Text(e, Modifier.padding(start = 8.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Bad)
                        }
                    }
                    InputBar()
                }
                if (showList) ChatList()
            }
        }
    }

    /** The red orb (.ai-orb): an accent gradient circle with the sparkles. */
    @Composable
    private fun Orb(size: androidx.compose.ui.unit.Dp) {
        val red = NoCap.Red
        Box(
            Modifier.size(size).clip(CircleShape).background(Brush.linearGradient(listOf(red, androidx.compose.ui.graphics.lerp(red, Color.Black, 0.45f)))),
            contentAlignment = Alignment.Center,
        ) { NcIcon("sparkles", Color.White, size * 0.5f) }
    }

    @Composable
    private fun TopBar() {
        Column {
            Row(Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                ShellIconButton("menu", t("Chats", "Conversations")) { showList = !showList }
                Column(Modifier.weight(1f).padding(horizontal = 10.dp)) {
                    Text(
                        conversations.firstOrNull { it.id == current }?.title?.ifBlank { null } ?: t("New chat", "New chat"),
                        style = MaterialTheme.typography.titleSmall.copy(fontSize = 15.sp, fontWeight = FontWeight.SemiBold), maxLines = 1, overflow = TextOverflow.Ellipsis,
                    )
                    Text("NoCap AI", style = MaterialTheme.typography.bodySmall.copy(fontSize = 12.sp), color = NoCap.Muted, maxLines = 1)
                }
                ShellIconButton("plus", t("New chat", "New chat"), tint = NoCap.Red) { newChat() }
                Spacer(Modifier.width(6.dp))
                ShellIconButton("close", t("Close", "Close")) { finish() }
            }
            Box(Modifier.fillMaxWidth().height(1.dp).background(Shell.edge))
        }
    }

    @Composable
    private fun Chat() {
        val state = rememberLazyListState()
        val keyboard = WindowInsets.ime.getBottom(LocalDensity.current) > 0
        // Follow the reply to its last line, but only while you're already at the bottom.
        LaunchedEffect(messages.size, messages.lastOrNull()?.content?.length, keyboard) {
            if (messages.isEmpty()) return@LaunchedEffect
            val seen = state.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: -1
            if (seen < 0 || seen >= messages.lastIndex - 2) state.scrollToItem(messages.lastIndex, Int.MAX_VALUE)
        }
        if (messages.isEmpty()) {
            Column(
                Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp, vertical = 16.dp),
                verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Orb(68.dp)
                Spacer(Modifier.height(12.dp))
                Text(t("What's the move?", "How can I help?"), style = MaterialTheme.typography.titleMedium.copy(fontSize = 22.sp), textAlign = TextAlign.Center)
                Spacer(Modifier.height(6.dp))
                Text(
                    t("Ask anything. It runs on your own NoCap server, with your memory and rules. No cap.", "Ask anything. NoCap AI runs on your NoCap server with your saved memory and rules."),
                    style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted, textAlign = TextAlign.Center,
                )
                Spacer(Modifier.height(14.dp))
                listOf(
                    t("What's eating all my disk space?", "Explain what is using the most disk space on a server"),
                    t("Write me a script that backs up a folder every night", "Write a shell script that backs up a folder every night"),
                    t("Give me a checklist to lock down my home server", "Give me a checklist for securing a home server"),
                ).forEach { s ->
                    Text(
                        s,
                        Modifier.padding(vertical = 4.dp).widthIn(max = 520.dp).clip(RoundedCornerShape(50)).background(Shell.hover).border(1.dp, Shell.edge, RoundedCornerShape(50))
                            .clickable { input = s; send() }.padding(horizontal = 14.dp, vertical = 8.dp),
                        style = MaterialTheme.typography.bodyMedium.copy(fontSize = 14.sp), color = NoCap.Text,
                    )
                }
            }
            return
        }
        val clip = LocalClipboardManager.current
        LazyColumn(state = state, contentPadding = PaddingValues(horizontal = 14.dp, vertical = 14.dp), verticalArrangement = Arrangement.spacedBy(14.dp), modifier = Modifier.fillMaxSize()) {
            itemsIndexed(messages) { i, m ->
                val mine = m.role == "user"
                val text = when {
                    m.error.isNotBlank() && m.content.isBlank() -> m.error
                    m.content.isEmpty() && thinking && i == messages.lastIndex -> t("Cooking…", "Thinking…")
                    else -> m.content
                }
                if (mine) {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                        Box(
                            Modifier.widthIn(max = 320.dp).clip(RoundedCornerShape(topStart = 16.dp, topEnd = 16.dp, bottomEnd = 4.dp, bottomStart = 16.dp))
                                .background(NoCap.Red).padding(horizontal = 14.dp, vertical = 9.dp),
                        ) { SelectionContainer { Text(text, style = MaterialTheme.typography.bodyLarge, color = Color.White) } }
                    }
                } else {
                    Row(Modifier.fillMaxWidth()) {
                        Orb(28.dp)
                        Column(Modifier.weight(1f).padding(start = 12.dp)) {
                            val failed = m.error.isNotBlank() && m.content.isBlank()
                            if (failed) {
                                Row(Modifier.clip(RoundedCornerShape(12.dp)).background(NoCap.Bad.copy(alpha = 0.1f)).padding(horizontal = 12.dp, vertical = 9.dp)) {
                                    NcIcon("alert", NoCap.Bad, 16.dp)
                                    Text(text, Modifier.padding(start = 8.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Bad)
                                }
                            } else {
                                SelectionContainer { Text(text, style = MaterialTheme.typography.bodyLarge, color = if (m.content.isEmpty()) NoCap.Muted else NoCap.Text) }
                            }
                            // Message actions, like the web's (shown for finished replies).
                            if (m.content.isNotEmpty() && !(thinking && i == messages.lastIndex)) {
                                Row(Modifier.padding(top = 4.dp)) {
                                    Box(
                                        Modifier.size(30.dp).clip(CircleShape).clickable {
                                            clip.setText(AnnotatedString(m.content))
                                            error = null
                                        },
                                        contentAlignment = Alignment.Center,
                                    ) { NcIcon("copy", NoCap.Muted, 16.dp, desc = t("Copy", "Copy")) }
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    /** The chat list as a drawer from the left (.ai-sidebar.drawer): new chat, search, your chats. */
    @Composable
    private fun ChatList() {
        var query by remember { mutableStateOf("") }
        Box(Modifier.fillMaxSize()) {
            Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.35f)).clickable(remember { MutableInteractionSource() }, null) { showList = false })
            Column(
                Modifier.fillMaxHeight().widthIn(max = 320.dp).fillMaxWidth(0.84f)
                    .shellGlass(RoundedCornerShape(topEnd = 22.dp, bottomEnd = 22.dp), GlassLevel.MENU)
                    .clickable(remember { MutableInteractionSource() }, null) {}.padding(12.dp),
            ) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    PillButton(t("New chat", "New chat"), { newChat() }, Modifier.weight(1f), icon = "plus")
                    Spacer(Modifier.width(8.dp))
                    ShellIconButton("close", t("Close", "Close")) { showList = false }
                }
                Spacer(Modifier.height(10.dp))
                Row(
                    Modifier.fillMaxWidth().height(40.dp).clip(RoundedCornerShape(10.dp)).background(Shell.hover).border(1.dp, Shell.edge, RoundedCornerShape(10.dp)).padding(horizontal = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    NcIcon("search", NoCap.Muted, 16.dp)
                    Box(Modifier.weight(1f).padding(start = 8.dp), contentAlignment = Alignment.CenterStart) {
                        if (query.isEmpty()) Text(t("Search your chats", "Search chats"), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted, maxLines = 1)
                        BasicTextField(query, { query = it }, Modifier.fillMaxWidth(), singleLine = true, textStyle = MaterialTheme.typography.bodyMedium.copy(color = NoCap.Text), cursorBrush = SolidColor(NoCap.Red))
                    }
                }
                Spacer(Modifier.height(10.dp))
                val q = query.trim().lowercase()
                val list = if (q.isEmpty()) conversations else conversations.filter { q in it.title.lowercase() || q in it.preview.lowercase() }
                if (list.isEmpty()) Text(if (q.isEmpty()) t("No chats yet.", "No conversations yet.") else t("No chats match.", "No matching chats."), Modifier.padding(6.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
                LazyColumn(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    items(list, key = { it.id }) { c ->
                        val on = c.id == current
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(if (on) Shell.hover else Color.Transparent)
                                .clickable { open(c.id) }.padding(start = 10.dp, top = 6.dp, bottom = 6.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(c.title.ifBlank { t("Untitled", "Untitled") }, style = MaterialTheme.typography.titleSmall.copy(fontSize = 14.sp), maxLines = 1, overflow = TextOverflow.Ellipsis)
                                if (c.preview.isNotBlank()) Text(c.preview, style = MaterialTheme.typography.bodySmall.copy(fontSize = 12.sp), maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                            Box(Modifier.size(36.dp).clip(CircleShape).clickable { delete(c.id) }, contentAlignment = Alignment.Center) {
                                NcIcon("trash", NoCap.Muted, 16.dp, desc = t("Delete", "Delete"))
                            }
                        }
                    }
                }
            }
        }
    }

    /** The compose box (.ai-compose): rounded, the text, then voice and the round send button. */
    @Composable
    private fun InputBar() {
        val shape = RoundedCornerShape(16.dp)
        Row(
            Modifier.fillMaxWidth().padding(start = 12.dp, end = 12.dp, top = 6.dp, bottom = 10.dp)
                .clip(shape).background(Shell.tint(GlassLevel.MENU)).border(1.dp, if (input.isNotEmpty()) NoCap.Red.copy(alpha = 0.6f) else Shell.edge, shape)
                .padding(start = 14.dp, end = 6.dp, top = 4.dp, bottom = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(Modifier.weight(1f).heightIn(min = 44.dp), contentAlignment = Alignment.CenterStart) {
                if (input.isEmpty()) Text(t("Ask NoCap AI…", "Message NoCap AI"), style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted)
                BasicTextField(input, { input = it }, Modifier.fillMaxWidth().padding(vertical = 10.dp), textStyle = MaterialTheme.typography.bodyLarge.copy(color = NoCap.Text), cursorBrush = SolidColor(NoCap.Red), maxLines = 5)
            }
            if (voiceAvailable()) {
                Box(Modifier.size(40.dp).clip(CircleShape).clickable { voice() }, contentAlignment = Alignment.Center) { MicGlyph() }
            }
            val ready = input.isNotBlank() && !thinking
            Box(
                Modifier.size(38.dp).clip(CircleShape).background(if (ready) NoCap.Red else NoCap.Text.copy(alpha = 0.12f)).clickable(enabled = ready) { send() },
                contentAlignment = Alignment.Center,
            ) { NcIcon("arrowUp", if (ready) Color.White else NoCap.Muted, 18.dp, desc = t("Send", "Send")) }
        }
    }

    @Composable
    private fun MicGlyph() {
        androidx.compose.foundation.Canvas(Modifier.size(20.dp)) {
            val w = size.width
            val h = size.height
            drawRoundRect(NoCap.Text, topLeft = androidx.compose.ui.geometry.Offset(w * 0.35f, 0f), size = androidx.compose.ui.geometry.Size(w * 0.3f, h * 0.6f), cornerRadius = androidx.compose.ui.geometry.CornerRadius(w * 0.15f))
            drawArc(NoCap.Text, 0f, 180f, false, topLeft = androidx.compose.ui.geometry.Offset(w * 0.2f, h * 0.2f), size = androidx.compose.ui.geometry.Size(w * 0.6f, h * 0.55f), style = androidx.compose.ui.graphics.drawscope.Stroke(w * 0.08f))
            drawLine(NoCap.Text, androidx.compose.ui.geometry.Offset(w * 0.5f, h * 0.75f), androidx.compose.ui.geometry.Offset(w * 0.5f, h * 0.95f), strokeWidth = w * 0.08f)
        }
    }

    /** What survives closing the AI sheet, for this app session. */
    private object Kept {
        var current: String? = null
        var input: String = ""
        var messages: List<ChatMessage> = emptyList()
    }

    companion object {
        const val EXTRA_PROMPT = "prompt"
    }
}

