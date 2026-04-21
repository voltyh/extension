chrome.runtime.onMessage.addListener((request, sender, sendResponse) => {
    if (request.action === "fetchBase64") {
        fetch(request.url)
            .then(response => response.blob())
            .then(blob => {
                const reader = new FileReader();
                reader.onloadend = () => sendResponse({ base64: reader.result });
                reader.readAsDataURL(blob);
            })
            .catch(error => {
                console.error("Background fetch error:", error);
                sendResponse({ base64: null });
            });
        return true; // Keeps the message channel open for the async response
    }
});