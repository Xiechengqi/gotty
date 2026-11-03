// Keep the unload guard scoped to a live connection. Call dispose before an
// intentional reload (for example after restarting the server).
export function createConfirmClose(enabled: boolean) {
    let armed = false;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
        event.preventDefault();
        event.returnValue = "";
    };
    const setConnected = (connected: boolean) => {
        const next = enabled && connected;
        if (next === armed) return;
        armed = next;
        if (armed) window.addEventListener("beforeunload", onBeforeUnload);
        else window.removeEventListener("beforeunload", onBeforeUnload);
    };
    return { setConnected, dispose: () => setConnected(false) };
}
