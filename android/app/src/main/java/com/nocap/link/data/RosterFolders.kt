package com.nocap.link.data

import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

/** A folder in the app drawer ("the roster"), like the web OS launcher's folders. */
data class RosterFolder(val id: String, val name: String, val keys: List<String>)

/**
 * The roster's folders, saved with the other launcher settings. The
 * functions on lists are pure (and unit-tested); [Opt.rosterFolders] holds
 * the saved JSON.
 */
object RosterFolders {
    const val MAX_NAME = 40

    fun decode(s: String): List<RosterFolder> = runCatching {
        val a = JSONArray(s)
        (0 until a.length()).mapNotNull { i ->
            val o = a.optJSONObject(i) ?: return@mapNotNull null
            val keys = o.optJSONArray("keys")?.let { k -> (0 until k.length()).map { k.optString(it) } }.orEmpty().filter { it.isNotBlank() }.distinct()
            val id = o.optString("id").ifBlank { return@mapNotNull null }
            RosterFolder(id, o.optString("name").take(MAX_NAME), keys)
        }.distinctBy { it.id }
    }.getOrDefault(emptyList())

    fun encode(list: List<RosterFolder>): String = JSONArray().apply {
        list.forEach { f -> put(JSONObject().put("id", f.id).put("name", f.name).put("keys", JSONArray(f.keys))) }
    }.toString()

    /** A new folder holding [keys] (taken out of any other folder). */
    fun create(list: List<RosterFolder>, name: String, keys: List<String>, id: String = UUID.randomUUID().toString().take(10)): List<RosterFolder> {
        val clean = keys.distinct()
        return without(list, clean) + RosterFolder(id, name.trim().take(MAX_NAME), clean)
    }

    /** Puts [key] in folder [id] (and out of any other). */
    fun add(list: List<RosterFolder>, id: String, key: String): List<RosterFolder> {
        val target = list.firstOrNull { it.id == id } ?: return list
        if (key in target.keys) return list
        return without(list, listOf(key)).map { if (it.id == id) it.copy(keys = it.keys + key) else it }
    }

    /** Takes [key] out of every folder; folders left empty go away. */
    fun remove(list: List<RosterFolder>, key: String): List<RosterFolder> = without(list, listOf(key))

    fun rename(list: List<RosterFolder>, id: String, name: String): List<RosterFolder> =
        list.map { if (it.id == id && name.isNotBlank()) it.copy(name = name.trim().take(MAX_NAME)) else it }

    /** Deletes a folder; its apps go back to the roster. */
    fun delete(list: List<RosterFolder>, id: String): List<RosterFolder> = list.filter { it.id != id }

    /** Drops apps that are gone (uninstalled or hidden) and folders left empty. */
    fun prune(list: List<RosterFolder>, present: Set<String>): List<RosterFolder> =
        list.map { it.copy(keys = it.keys.filter { k -> k in present }) }.filter { it.keys.isNotEmpty() }

    /** Every app key that sits in a folder. */
    fun inFolders(list: List<RosterFolder>): Set<String> = list.flatMap { it.keys }.toSet()

    private fun without(list: List<RosterFolder>, keys: List<String>): List<RosterFolder> =
        list.map { f -> f.copy(keys = f.keys.filter { it !in keys }) }.filter { it.keys.isNotEmpty() }

    // ---- saved state ----

    val all: List<RosterFolder> get() = decode(Opt.rosterFolders.v)

    fun save(list: List<RosterFolder>) = Opt.rosterFolders.set(encode(list))
}
