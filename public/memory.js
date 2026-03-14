export class MemoryManager {
    constructor() {
        this.isClientSide = localStorage.getItem('speax_client_storage') === 'true';
        this.activeId = "default";
        this.threads = {
            "default": { id: "default", name: "General Chat", history: [], archive: [], summary: "" }
        };
        this.loadFromDisk();
    }

    loadFromDisk() {
        if (!this.isClientSide) return;
        const data = localStorage.getItem('speax_memory_v2');
        if (data) {
            const parsed = JSON.parse(data);
            this.activeId = parsed.activeId;
            this.threads = parsed.threads;
            return;
        }
        
        // Migrate legacy single-thread client-side memory
        const oldData = localStorage.getItem('speax_memory');
        if (oldData) {
            const parsed = JSON.parse(oldData);
            this.activeId = "default";
            this.threads = {
                "default": { id: "default", name: "General Chat", history: parsed.history || [], archive: parsed.archive || [], summary: parsed.summary || "" }
            };
            this.saveToDisk();
        }
    }

    saveToDisk() {
        if (!this.isClientSide) return;
        localStorage.setItem('speax_memory_v2', JSON.stringify({ activeId: this.activeId, threads: this.threads }));
    }

    updateThreads(activeId, threadList) {
        this.activeId = activeId;
        const safeList = threadList || [];
        // Ensure thread objects exist locally
        safeList.forEach(t => {
            if (!this.threads[t.id]) {
                this.threads[t.id] = { id: t.id, name: t.name, history: [], archive: [], summary: "" };
            } else {
                this.threads[t.id].name = t.name;
            }
        });
        // Cleanup threads that no longer exist on server
        const validIds = new Set(safeList.map(t => t.id));
        Object.keys(this.threads).forEach(id => {
            if (!validIds.has(id)) delete this.threads[id];
        });
        this.saveToDisk();
    }

    updateActiveMemory(history, archive, summary) {
        if (!this.threads[this.activeId]) return;
        if (history !== undefined) this.threads[this.activeId].history = history;
        if (archive !== undefined) this.threads[this.activeId].archive = archive;
        if (summary !== undefined) this.threads[this.activeId].summary = summary;
        this.saveToDisk();
    }

    getFullState() {
        return { activeId: this.activeId, threads: this.threads };
    }

    clear() {
        localStorage.removeItem('speax_memory_v2');
        localStorage.removeItem('speax_memory');
        this.activeId = "default";
        this.threads = {
            "default": { id: "default", name: "General Chat", history: [], archive: [], summary: "" }
        };
    }

    setClientSide(isClient) {
        this.isClientSide = isClient;
        localStorage.setItem('speax_client_storage', isClient);
        if (!isClient) {
            this.clear();
        }
    }
}
