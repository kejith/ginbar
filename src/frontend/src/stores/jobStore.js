import { create } from "zustand";
import api from "../utils/api.js";

/**
 * Job store — connects to the server's job SSE stream and provides
 * live snapshots of all running/completed jobs.
 *
 * Usage:
 *   const { jobs, connect, disconnect, cancelJob } = useJobStore();
 *   useEffect(() => { connect(isAdmin); return () => disconnect(); }, []);
 */
const useJobStore = create((set, get) => ({
    jobs: [],
    connected: false,
    _es: null,

    /**
     * Connect to the job SSE stream.
     * @param {boolean} isAdmin — if true, connects to the admin endpoint
     *                            which returns ALL jobs; otherwise the
     *                            authenticated-user endpoint (own + global).
     */
    connect: (isAdmin = false) => {
        // Don't double-connect.
        if (get()._es) return;

        const path = isAdmin
            ? "/api/admin/jobs/stream"
            : "/api/jobs/stream";

        const es = new EventSource(path, { withCredentials: true });

        es.addEventListener("message", (e) => {
            try {
                const data = JSON.parse(e.data);
                set({ jobs: data.jobs ?? [] });
            } catch (_) { }
        });

        es.addEventListener("error", () => {
            // On error, close and reconnect after a delay.
            es.close();
            set({ _es: null, connected: false });
            setTimeout(() => {
                if (!get()._es) get().connect(isAdmin);
            }, 5000);
        });

        es.addEventListener("open", () => {
            set({ connected: true });
        });

        set({ _es: es, connected: true });
    },

    /**
     * Disconnect from the SSE stream.
     */
    disconnect: () => {
        const es = get()._es;
        if (es) {
            es.close();
            set({ _es: null, connected: false });
        }
    },

    /**
     * Cancel a job by ID (admin-only).
     * @param {string} id
     */
    cancelJob: async (id) => {
        await api.post(`/admin/jobs/${id}/cancel`);
    },

    /**
     * Fetch jobs once (REST fallback, admin-only).
     */
    fetchJobs: async () => {
        const { data } = await api.get("/admin/jobs");
        set({ jobs: data.jobs ?? [] });
    },
}));

export default useJobStore;
