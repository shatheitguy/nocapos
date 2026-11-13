package com.nocap.link.ai

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
        intent.getStringExtra(EXTRA_PROMPT)?.let { input = it }
        setContent { NoCapTheme { Screen() } }
        if (api != null) loadList(pickLatest = intent.getStringExtra(EXTRA_PROMPT) == null)
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

    @Composable
    private fun Screen() {
        BackHandler(showList) { showList = false }
        Wallpaper {
            // Clear of status/nav bars, cutouts and the keyboard, so nothing sits under them.
            Column(Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing)) {
                Row(Modifier.fillMaxWidth().padding(start = 4.dp, end = 4.dp, top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                    IconButton(onClick = { showList = !showList }) { Icon(Icons.Filled.List, t("Chats", "Conversations"), tint = NoCap.Text) }
                    Column(Modifier.weight(1f)) {
                        Text("NOCAP AI", style = MaterialTheme.typography.headlineSmall)
                        Text(conversations.firstOrNull { it.id == current }?.title?.ifBlank { null } ?: t("New chat", "New conversation"), style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    IconButton(onClick = { newChat() }) { Icon(Icons.Filled.Add, t("New chat", "New conversation"), tint = NoCap.Red) }
                    IconButton(onClick = { finish() }) { Icon(Icons.Filled.Close, t("Close", "Close"), tint = NoCap.Text) }
                }
                if (api == null) {
                    Column(Modifier.weight(1f).fillMaxWidth().padding(24.dp), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(t("NoCap AI lives on your NoCap server. Link this phone first.", "NoCap AI runs on your NoCap server. Link this phone first."), style = MaterialTheme.typography.bodyLarge)
                        Spacer(Modifier.height(16.dp))
                        RedButton(t("Link with NoCap Link", "Link with NoCap Link"), {
                            startActivity(Intent(this@AiActivity, MainActivity::class.java).setAction(MainActivity.ACTION_HUB))
                            finish()
                        })
                    }
                    return@Column
                }
                Box(Modifier.weight(1f).fillMaxWidth()) {
                    Chat()
                    if (showList) ChatList()
                }
                error?.let { Text(it, Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Warn) }
                InputBar()
            }
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
            Column(Modifier.fillMaxSize().padding(28.dp), verticalArrangement = Arrangement.Center) {
                Text(t("What's good?", "How can I help?"), style = MaterialTheme.typography.displaySmall)
                Spacer(Modifier.height(8.dp))
                Text(t("Ask anything. It runs on your own NoCap server, with your memory and rules.", "Ask anything. NoCap AI runs on your NoCap server with your saved memory and rules."), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
            }
            return
        }
        LazyColumn(state = state, contentPadding = PaddingValues(14.dp), verticalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.fillMaxSize()) {
            itemsIndexed(messages) { i, m ->
                val mine = m.role == "user"
                Row(Modifier.fillMaxWidth(), horizontalArrangement = if (mine) Arrangement.End else Arrangement.Start) {
                    Box(
                        Modifier.widthIn(max = 320.dp).clip(RoundedCornerShape(16.dp))
                            .background(if (mine) NoCap.Red.copy(alpha = 0.85f) else NoCap.Card)
                            .padding(horizontal = 14.dp, vertical = 10.dp),
                    ) {
                        val text = when {
                            m.error.isNotBlank() && m.content.isBlank() -> "⚠ " + m.error
                            m.content.isEmpty() && thinking && i == messages.lastIndex -> t("Cooking…", "Thinking…")
                            else -> m.content
                        }
                        SelectionContainer { Text(text, style = MaterialTheme.typography.bodyLarge, color = if (mine) Color.White else NoCap.Text) }
                    }
                }
            }
        }
    }

    @Composable
    private fun ChatList() {
        Column(Modifier.fillMaxSize().background(NoCap.Sheet).padding(12.dp)) {
            Label(t("Your chats", "Conversations"))
            Spacer(Modifier.height(8.dp))
            if (conversations.isEmpty()) Text(t("No chats yet.", "No conversations yet."), style = MaterialTheme.typography.bodyMedium)
            LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                items(conversations, key = { it.id }) { c ->
                    Row(
                        Modifier.fillMaxWidth().glass(RoundedCornerShape(14.dp), edge = if (c.id == current) NoCap.Red else NoCap.GlassEdge)
                            .clickable { open(c.id) }.padding(start = 14.dp, top = 8.dp, bottom = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Column(Modifier.weight(1f)) {
                            Text(c.title.ifBlank { t("Untitled", "Untitled") }, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            if (c.preview.isNotBlank()) Text(c.preview, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                        IconButton(onClick = { delete(c.id) }) { Icon(Icons.Filled.Delete, t("Delete", "Delete"), tint = NoCap.Muted) }
                    }
                }
            }
        }
    }

    @Composable
    private fun InputBar() {
        Row(
            Modifier.fillMaxWidth().padding(10.dp).glass(RoundedCornerShape(24.dp), strong = true).padding(start = 16.dp, end = 6.dp, top = 4.dp, bottom = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(Modifier.weight(1f).heightIn(min = 44.dp), contentAlignment = Alignment.CenterStart) {
                if (input.isEmpty()) Text(t("Ask NoCap AI…", "Message NoCap AI"), style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted)
                BasicTextField(input, { input = it }, Modifier.fillMaxWidth().padding(vertical = 10.dp), textStyle = MaterialTheme.typography.bodyLarge.copy(color = NoCap.Text), cursorBrush = SolidColor(NoCap.Red), maxLines = 5)
            }
            if (voiceAvailable()) {
                Box(Modifier.size(40.dp).clip(CircleShape).clickable { voice() }, contentAlignment = Alignment.Center) { MicGlyph() }
            }
            Box(
                Modifier.size(40.dp).clip(CircleShape).background(if (input.isNotBlank() && !thinking) NoCap.Red else Color(0x33FFFFFF)).clickable { send() },
                contentAlignment = Alignment.Center,
            ) { Icon(Icons.Filled.Send, t("Send", "Send"), tint = Color.White, modifier = Modifier.size(18.dp)) }
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

    companion object {
        const val EXTRA_PROMPT = "prompt"
    }
}

