let socket;
let outContext;
let inContext;
let processor;
let input;

let audioChunks = [];
let audioQueue = [];
let isPlayingAudio = false;
let currentAiDiv = null;
let currentAudioSource = null;
let audioAnalyser = null;
let inputAnalyser = null;
let inputVisualizerId = null;
let visualizerId = null;
let preRollBuffer = [];
let isSpeaking = false;
let silenceFrames = 0;
let wakeLock = null;
let isMuted = false;
let reconnectDelay = 1000;
const MAX_RECONNECT_DELAY = 5000;
let isDictationActive = false;
let isPaused = false;

const NOISE_THRESHOLD = 0.015; // RMS threshold. Increase if your room is noisy!
const SILENCE_FRAMES_LIMIT = 6; // ~0.75 seconds of trailing silence to stop
const PRE_ROLL_FRAMES = 2; // ~0.5 seconds of audio to keep BEFORE speech is detected
const MIN_CHUNKS = 2; // Require at least ~0.5 seconds of audio to bother sending

const ICON_POWER = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width: 50%; height: 50%;"><path d="M18.36 6.64a9 9 0 1 1-12.73 0"></path><line x1="12" y1="2" x2="12" y2="12"></line></svg>`;
const ICON_WAVE = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width: 50%; height: 50%;"><path d="M12 2v20M17 7v10M22 10v4M7 7v10M2 10v4"></path></svg>`;
const ICON_WAIT = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width: 50%; height: 50%;"><path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83"></path></svg>`;
const ICON_MIC = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width: 50%; height: 50%;"><path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"></path><path d="M19 10v2a7 7 0 0 1-14 0v-2"></path><line x1="12" y1="19" x2="12" y2="22"></line></svg>`;
const ICON_MIC_MUTE = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width: 50%; height: 50%;"><line x1="2" y1="2" x2="22" y2="22"></line><path d="M18.89 13.23A7.12 7.12 0 0 0 19 12v-2"></path><path d="M5 10v2a7 7 0 0 0 12 5"></path><path d="M15 9.34V5a3 3 0 0 0-5.68-1.33"></path><path d="M9 9v3a3 3 0 0 0 5.12 2.12"></path><line x1="12" y1="19" x2="12" y2="22"></line></svg>`;
const ICON_PAUSE = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width: 50%; height: 50%;"><rect x="6" y="4" width="4" height="16"></rect><rect x="14" y="4" width="4" height="16"></rect></svg>`;
const ICON_PLAY = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width: 50%; height: 50%;"><polygon points="5 3 19 12 5 21 5 3"></polygon></svg>`;

const status = document.getElementById('status');
const mainToggleBtn = document.getElementById('mainToggleBtn');
const transcript = document.getElementById('transcript');
const homeTabBtn = document.getElementById('tab-home');
const threadTabBtn = document.getElementById('tab-thread');
const memoryTabBtn = document.getElementById('tab-memory');
const homeView = document.getElementById('view-home');
const summaryView = document.getElementById('summary-view');
const summaryContent = document.getElementById('summary-content');
const authSection = document.getElementById('auth-section');
const appSection = document.getElementById('app-section');
const loginBtn = document.getElementById('loginBtn');
const clearHistoryBtn = document.getElementById('clearHistoryBtn');
const rebuildSummaryBtn = document.getElementById('rebuildSummaryBtn');
const muteBtn = document.getElementById('muteBtn');
const pauseBtn = document.getElementById('pauseBtn');

const progressContainer = document.getElementById('audioProgressContainer');
const progressBar = document.getElementById('audioProgressBar');
let aiAudioTotalSecs = 0;
let aiAudioPlayedSecs = 0;
let aiChunkStartContextTime = 0;
let aiChunkDuration = 0;
let progressAnimId = null;
let currentVisualPercent = 0;
let progressLogThrottler = 0;

const userProfileContainer = document.getElementById('userProfileContainer');
const userAvatarBtn = document.getElementById('userAvatarBtn');
const userDropdown = document.getElementById('userDropdown');
const menuSettingsBtn = document.getElementById('menuSettingsBtn');
const menuLogoutBtn = document.getElementById('menuLogoutBtn');
const settingsFab = document.getElementById('settingsFab');
const settingsSidebar = document.getElementById('settingsSidebar');
const closeSettingsBtn = document.getElementById('closeSettingsBtn');
const userName = document.getElementById('userName');
const userBio = document.getElementById('userBio');
const aiProvider = document.getElementById('aiProvider');
const geminiSettings = document.getElementById('geminiSettings');
const geminiApiKey = document.getElementById('geminiApiKey');
const aiModel = document.getElementById('aiModel');
const aiVoice = document.getElementById('aiVoice');
const saveSettingsBtn = document.getElementById('saveSettingsBtn');

aiProvider.value = localStorage.getItem('speax_provider') || 'ollama';
geminiApiKey.value = localStorage.getItem('speax_gemini_key') || '';
userName.value = localStorage.getItem('speax_user_name') || '';
userBio.value = localStorage.getItem('speax_user_bio') || '';
if (aiProvider.value === 'gemini') geminiSettings.style.display = 'flex';

async function loadModels() {
    const provider = aiProvider.value;
    const apiKey = geminiApiKey.value;
    
    if (provider === 'gemini' && !apiKey) {
        aiModel.innerHTML = '<option value="">Enter API Key first...</option>';
        return;
    }

    aiModel.innerHTML = '<option value="">Loading models...</option>';
    try {
        const res = await fetch(`/api/models?provider=${provider}&apiKey=${apiKey}`);
        const models = await res.json();
        aiModel.innerHTML = '';
        if (models && models.length > 0) {
            models.forEach(m => {
                const opt = document.createElement('option');
                opt.value = m.id;
                opt.innerText = m.name;
                aiModel.appendChild(opt);
            });
            const savedModel = localStorage.getItem('speax_model');
            if (savedModel && Array.from(aiModel.options).some(opt => opt.value === savedModel)) {
                aiModel.value = savedModel;
            }
        } else {
            aiModel.innerHTML = '<option value="">No models found</option>';
        }
    } catch (e) {
        aiModel.innerHTML = '<option value="">Error loading models</option>';
    }
}

async function loadVoices() {
    aiVoice.innerHTML = '<option value="">Loading voices...</option>';
    try {
        const res = await fetch('/api/voices');
        const voices = await res.json();
        aiVoice.innerHTML = '';
        if (voices && voices.length > 0) {
            voices.forEach(v => {
                const opt = document.createElement('option');
                opt.value = v;
                opt.innerText = v.replace('.onnx', '');
                aiVoice.appendChild(opt);
            });
            const savedVoice = localStorage.getItem('speax_voice');
            if (savedVoice && Array.from(aiVoice.options).some(opt => opt.value === savedVoice)) {
                aiVoice.value = savedVoice;
            }
        } else {
            aiVoice.innerHTML = '<option value="">No voices found</option>';
        }
    } catch (e) {
        aiVoice.innerHTML = '<option value="">Error loading voices</option>';
    }
}

settingsFab.onclick = () => settingsSidebar.style.right = '0';
closeSettingsBtn.onclick = () => settingsSidebar.style.right = '-320px';
aiProvider.onchange = () => { geminiSettings.style.display = aiProvider.value === 'gemini' ? 'flex' : 'none'; loadModels(); };
geminiApiKey.onblur = () => { if (aiProvider.value === 'gemini') loadModels(); };

saveSettingsBtn.onclick = () => {
    localStorage.setItem('speax_provider', aiProvider.value);
    localStorage.setItem('speax_gemini_key', geminiApiKey.value);
    localStorage.setItem('speax_model', aiModel.value);
    localStorage.setItem('speax_voice', aiVoice.value);
    localStorage.setItem('speax_user_name', userName.value);
    localStorage.setItem('speax_user_bio', userBio.value);
    settingsSidebar.style.right = '-320px';
    if (socket && socket.readyState === WebSocket.OPEN) {
        socket.send(`[SETTINGS]${JSON.stringify({ 
            userName: userName.value,
            userBio: userBio.value,
            provider: aiProvider.value, 
            apiKey: geminiApiKey.value, 
            model: aiModel.value,
            voice: aiVoice.value
        })}`);
    }
};

loadModels();
loadVoices();

if (document.cookie.includes('speax_session=')) {
    authSection.style.display = 'none';
    appSection.style.display = 'flex';
    settingsFab.style.display = 'flex';
    userProfileContainer.style.display = 'block';
    
    const avatarCookie = document.cookie.split('; ').find(row => row.startsWith('speax_avatar='));
    if (avatarCookie) {
        userAvatarBtn.src = decodeURIComponent(avatarCookie.split('=')[1]);
    } else {
        userAvatarBtn.src = 'data:image/svg+xml;utf8,<svg xmlns="http://www.w3.org/2000/svg" fill="%23aaa" viewBox="0 0 24 24"><path d="M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z"/></svg>';
    }
} else {
    authSection.style.display = 'block';
    appSection.style.display = 'none';
    settingsFab.style.display = 'none';
    userProfileContainer.style.display = 'none';
}

userAvatarBtn.onclick = (e) => {
    e.stopPropagation();
    userDropdown.style.display = userDropdown.style.display === 'flex' ? 'none' : 'flex';
};
document.addEventListener('click', () => userDropdown.style.display = 'none');
menuSettingsBtn.onclick = () => { settingsSidebar.style.right = '0'; };
menuLogoutBtn.onclick = () => {
    document.cookie = "speax_session=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/;";
    document.cookie = "speax_avatar=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/;";
    window.location.reload();
};

loginBtn.onclick = () => window.location.href = '/auth/login';

function switchTab(activeBtn, activeView) {
    [homeTabBtn, threadTabBtn, memoryTabBtn].forEach(btn => {
        if(btn) { btn.style.background = 'transparent'; btn.style.color = '#aaa'; }
    });
    if(activeBtn) { activeBtn.style.background = '#0e639c'; activeBtn.style.color = 'white'; }
    
    if(homeView) homeView.style.display = 'none';
    if(transcript) transcript.style.display = 'none';
    if(summaryView) summaryView.style.display = 'none';
    
    if(activeView) activeView.style.display = activeView === homeView ? 'flex' : 'block';
}

if(homeTabBtn) homeTabBtn.onclick = () => switchTab(homeTabBtn, homeView);
if(threadTabBtn) threadTabBtn.onclick = () => { switchTab(threadTabBtn, transcript); transcript.scrollTop = transcript.scrollHeight; };
if(memoryTabBtn) memoryTabBtn.onclick = () => switchTab(memoryTabBtn, summaryView);

async function requestWakeLock() {
    try {
        if ('wakeLock' in navigator) {
            wakeLock = await navigator.wakeLock.request('screen');
            console.log('Wake Lock acquired');
        }
    } catch (err) {
        console.error('Wake Lock error:', err);
    }
}

async function releaseWakeLock() {
    if (wakeLock !== null) {
        await wakeLock.release();
        wakeLock = null;
        console.log('Wake Lock released');
    }
}

document.addEventListener('visibilitychange', async () => {
    if (wakeLock !== null && document.visibilityState === 'visible' && isDictationActive) {
        await requestWakeLock();
    }
});

mainToggleBtn.onclick = async () => {
    if (isDictationActive) {
        stopEverything();
    } else {
        isDictationActive = true;
        reconnectDelay = 1000;
        mainToggleBtn.innerHTML = ICON_WAIT;
        mainToggleBtn.style.background = '#d7ba7d';
        connectWebSocket();
    }
};

function connectWebSocket() {
    if (!isDictationActive) return;
    status.innerText = "Status: Connecting...";
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    socket = new WebSocket(`${protocol}//${window.location.host}/ws`);
    
    socket.onopen = () => {
        status.innerText = isMuted ? "Status: Connected - Muted" : "Status: Connected - Listening...";
        mainToggleBtn.innerHTML = ICON_WAVE;
        mainToggleBtn.style.background = '#4ec9b0';
        muteBtn.style.display = 'flex';
        muteBtn.innerHTML = isMuted ? ICON_MIC_MUTE : ICON_MIC;
        pauseBtn.style.display = 'flex';
        pauseBtn.innerHTML = isPaused ? ICON_PLAY : ICON_PAUSE;
        reconnectDelay = 1000; // Reset delay on successful connection
        requestWakeLock();
        if (!processor) startRecording(); // Only start audio if not already running
    };

    socket.onmessage = async (event) => {
        // If the server sends us a Blob (Binary), it's Text-to-Speech audio!
        if (event.data instanceof Blob) {
            // Estimate duration: 16kHz 16-bit mono = 32,000 bytes per second
            const estSecs = Math.max(0, event.data.size - 44) / 32000;
            aiAudioTotalSecs += estSecs;
            console.log(`[Audio Rx] Chunk size: ${event.data.size} bytes. Est length: ${estSecs.toFixed(2)}s. New Total: ${aiAudioTotalSecs.toFixed(2)}s`);
            audioQueue.push(event.data);
            if (!isPlayingAudio) playNextAudio();
            return;
        }

        // Otherwise, it's our transcribed text from Whisper
        status.innerText = isMuted ? "Status: Connected - Muted" : "Status: Connected - Listening...";
        
        const rawText = event.data;

        if (rawText.startsWith("[SETTINGS_SYNC]")) {
            const s = JSON.parse(rawText.substring(15));
            if (s.provider) { // If server has state, overwrite local storage
                localStorage.setItem('speax_user_name', s.userName || '');
                localStorage.setItem('speax_user_bio', s.userBio || '');
                localStorage.setItem('speax_provider', s.provider);
                localStorage.setItem('speax_model', s.model || '');
                localStorage.setItem('speax_voice', s.voice || '');
                
                userName.value = s.userName || '';
                userBio.value = s.userBio || '';
                aiProvider.value = s.provider;
                geminiSettings.style.display = s.provider === 'gemini' ? 'flex' : 'none';
                
                loadModels().then(() => { if (s.model) aiModel.value = s.model; });
                loadVoices().then(() => { if (s.voice) aiVoice.value = s.voice; });
            }
            
            // Send combined state back to server (syncs local API key to ephemeral server session)
            socket.send(`[SETTINGS]${JSON.stringify({ 
                userName: localStorage.getItem('speax_user_name') || '',
                userBio: localStorage.getItem('speax_user_bio') || '',
                provider: localStorage.getItem('speax_provider') || 'ollama', 
                apiKey: localStorage.getItem('speax_gemini_key') || '',
                model: localStorage.getItem('speax_model') || '',
                voice: localStorage.getItem('speax_voice') || ''
            })}`);
            return;
        }

        if (rawText.startsWith("[HISTORY]")) {
            const historyData = JSON.parse(rawText.substring(9));
            
            // Capture scroll state before modifying the DOM
            const prevScrollTop = transcript.scrollTop;
            const wasAtBottom = transcript.scrollHeight - transcript.scrollTop <= transcript.clientHeight + 50;

            transcript.innerHTML = ''; // Clear board
            historyData.forEach((msg, idx) => {
                const msgDiv = document.createElement('div');
                msgDiv.style.marginBottom = '10px';
                msgDiv.style.display = 'flex';
                
                const delBtn = document.createElement('button');
                delBtn.innerText = 'X';
                delBtn.title = 'Delete this turn';
                delBtn.style.padding = '0';
                delBtn.style.margin = '0 0 0 10px';
                delBtn.style.height = '24px';
                delBtn.style.width = '24px';
                delBtn.style.borderRadius = '50%';
                delBtn.style.background = '#555';
                delBtn.style.color = '#ccc';
                delBtn.style.display = 'flex';
                delBtn.style.alignItems = 'center';
                delBtn.style.justifyContent = 'center';
                delBtn.style.flexShrink = '0';
                delBtn.onclick = () => {
                    if (socket && socket.readyState === WebSocket.OPEN) {
                        socket.send(`[DELETE_MSG]:${idx}`);
                    }
                };
                
                const contentSpan = document.createElement('span');
                contentSpan.style.whiteSpace = 'pre-wrap';
                contentSpan.style.wordBreak = 'break-word';
                if (msg.role === 'assistant') {
                    contentSpan.style.color = '#4ec9b0';
                    contentSpan.innerText = `Alyx: ${msg.content}`;
                } else {
                    const uName = userName.value || 'User';
                    contentSpan.innerText = `${uName}: ${msg.content}`;
                }
                
                msgDiv.appendChild(contentSpan);
                msgDiv.appendChild(delBtn);
                msgDiv.style.justifyContent = 'space-between';
                transcript.appendChild(msgDiv);
            });
            
            // Restore scroll state smoothly
            if (wasAtBottom) {
                transcript.scrollTop = transcript.scrollHeight;
            } else {
                transcript.scrollTop = prevScrollTop;
            }
            return;
        }

        if (rawText.startsWith("[SUMMARY]")) {
            try {
                const data = JSON.parse(rawText.substring(9));
                const ctxPct = Math.min(100, (data.estTokens / data.maxTokens) * 100);
                const arcPct = Math.min(100, (data.archiveTurns / data.maxArchiveTurns) * 100);
                
                // Minimized HTML string to avoid white-space: pre-wrap rendering issues
                summaryContent.innerHTML = `<div style="margin-bottom: 20px; background: #1e1e1e; padding: 12px; border-radius: 4px; border: 1px solid #333; white-space: normal;"><div style="margin-bottom: 12px;"><div style="display: flex; justify-content: space-between; font-size: 12px; margin-bottom: 4px; color: #aaa;"><span>Active Context Est. (Tokens)</span><span>${data.estTokens.toLocaleString()} / ${data.maxTokens.toLocaleString()}</span></div><div style="background: #333; height: 6px; border-radius: 3px; overflow: hidden;"><div style="background: #4ec9b0; height: 100%; width: ${ctxPct}%; transition: width 0.3s ease;"></div></div></div><div><div style="display: flex; justify-content: space-between; font-size: 12px; margin-bottom: 4px; color: #aaa;"><span>Archive Capacity (Turns)</span><span>${data.archiveTurns} / ${data.maxArchiveTurns}</span></div><div style="background: #333; height: 6px; border-radius: 3px; overflow: hidden;"><div style="background: #0e639c; height: 100%; width: ${arcPct}%; transition: width 0.3s ease;"></div></div></div></div><div style="font-size: 13px; color: #ce9178; margin-bottom: 8px; font-weight: bold; white-space: normal;">Summary of ${data.archiveTurns} older turns:</div><div style="line-height: 1.5; color: #d4d4d4;">${data.text || "No summary generated yet."}</div>`;
            } catch (e) {
                summaryContent.innerText = rawText.substring(9) || "No summary generated yet.";
            }
            return;
        }

        const text = rawText.trim();
        
        if (text === "[AI_START]") {
            aiAudioTotalSecs = 0;
            aiAudioPlayedSecs = 0;
            currentVisualPercent = 0;
            progressBar.style.transition = 'none';
            progressBar.style.width = '0%';
            currentAiDiv = document.createElement('div');
            currentAiDiv.style.color = '#4ec9b0';
            currentAiDiv.innerText = 'Alyx: ';
            transcript.appendChild(currentAiDiv);
            transcript.scrollTop = transcript.scrollHeight;
            return;
        } else if (text === "[AI_END]") {
            currentAiDiv = null;
            return;
        } else if (text === "[IGNORED]") {
            if (outContext && outContext.state === 'suspended') {
                outContext.resume();
            }
            return;
        }

        if (currentAiDiv) {
            currentAiDiv.innerText += rawText; // keep the spaces!
        } else if (text) {
            stopAudio(); // Abort the paused audio because we have valid STT
            const msg = document.createElement('div');
            const uName = userName.value || 'User';
            msg.innerText = `${uName}: ${text}`;
            transcript.appendChild(msg);
        }
        transcript.scrollTop = transcript.scrollHeight;
    };

    socket.onclose = () => {
        status.innerText = "Status: Disconnected";
        stopRecording();
        releaseWakeLock();
        
        if (isDictationActive) {
            status.innerText = `Status: Reconnecting in ${reconnectDelay / 1000}s...`;
            setTimeout(connectWebSocket, reconnectDelay);
            reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT_DELAY);
        } else {
            mainToggleBtn.innerHTML = ICON_POWER;
            mainToggleBtn.style.background = '#0e639c';
            mainToggleBtn.style.transform = 'scale(1)';
            mainToggleBtn.style.boxShadow = 'none';
            muteBtn.style.display = 'none';
            pauseBtn.style.display = 'none';
            isPaused = false;
        }
    };
}

muteBtn.onclick = () => {
    isMuted = !isMuted;
    if (isMuted) {
        muteBtn.innerHTML = ICON_MIC_MUTE;
        muteBtn.style.background = '#d16969';
        muteBtn.style.color = 'white';
        muteBtn.style.transform = 'scale(1)';
        muteBtn.style.boxShadow = 'none';
        if (!isPlayingAudio && socket && socket.readyState === WebSocket.OPEN) status.innerText = "Status: Connected - Muted";
    } else {
        muteBtn.innerHTML = ICON_MIC;
        muteBtn.style.background = '#d7ba7d';
        muteBtn.style.color = '#1e1e1e';
        if (!isPlayingAudio && socket && socket.readyState === WebSocket.OPEN) {
            status.innerText = "Status: Connected - Listening...";
            if (!inputVisualizerId && inputAnalyser) renderInputVisualizer();
        }
    }
};

clearHistoryBtn.onclick = () => {
    if (socket && socket.readyState === WebSocket.OPEN) {
        if (confirm("Are you sure you want to expunge all memory?")) {
            socket.send("[CLEAR_HISTORY]");
        }
    }
};

if (rebuildSummaryBtn) {
    rebuildSummaryBtn.onclick = () => {
        if (socket && socket.readyState === WebSocket.OPEN) {
            socket.send("[REBUILD_SUMMARY]");
            rebuildSummaryBtn.innerText = "Rebuilding...";
            setTimeout(() => { rebuildSummaryBtn.innerText = "Rebuild Summary"; }, 3000); // Visual reset
        }
    };
}

function stopEverything() {
    isDictationActive = false; // Prevent auto-reconnect
    if (socket && socket.readyState === WebSocket.OPEN) socket.close();
    mainToggleBtn.innerHTML = ICON_POWER;
    mainToggleBtn.style.background = '#0e639c';
    muteBtn.style.display = 'none';
    pauseBtn.style.display = 'none';
    isPaused = false;
    releaseWakeLock();
    stopRecording();
}

pauseBtn.onclick = () => {
    isPaused = !isPaused;
    if (isPaused) {
        pauseBtn.innerHTML = ICON_PLAY;
        pauseBtn.style.background = '#d7ba7d';
        pauseBtn.style.color = '#1e1e1e';
        if (outContext && outContext.state === 'running') outContext.suspend();
        if (!isMuted) muteBtn.click(); // Mute mic
        if (audioChunks.length > 0) sendAndClearBuffer(); // Flush any pending audio
        status.innerText = "Status: Paused";
    } else {
        pauseBtn.innerHTML = ICON_PAUSE;
        pauseBtn.style.background = '#555';
        pauseBtn.style.color = 'white';
        if (outContext && outContext.state === 'suspended') outContext.resume();
        if (isMuted) muteBtn.click(); // Unmute mic
        if (!isPlayingAudio && audioQueue.length > 0) playNextAudio(); // Drain the accumulated buffer
        if (isPlayingAudio && !progressAnimId) updateProgressBar(); // Resume progress UI
    }
};

async function playNextAudio() {
    if (audioQueue.length === 0 || isPaused) {
        isPlayingAudio = false;
        progressBar.style.width = '0%';
        status.innerText = isMuted ? "Status: Connected - Muted" : "Status: Connected - Listening...";
        return;
    }
    
    isPlayingAudio = true;
    status.innerText = "Status: Playing Audio...";
    
    if (!outContext) outContext = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 16000 });
    if (outContext.state === 'suspended') await outContext.resume();

    try {
        const blob = audioQueue.shift();
        const estSecs = Math.max(0, blob.size - 44) / 32000;
        const arrayBuffer = await blob.arrayBuffer();
        const audioBuffer = await outContext.decodeAudioData(arrayBuffer);
        
        // Correct our rough estimate with the precise decoded duration
        console.log(`[Audio Play] Decoded exact duration: ${audioBuffer.duration.toFixed(2)}s (Estimate was ${estSecs.toFixed(2)}s).`);
        aiAudioTotalSecs = aiAudioTotalSecs - estSecs + audioBuffer.duration;

        if (!audioAnalyser) {
            audioAnalyser = outContext.createAnalyser();
            audioAnalyser.fftSize = 256;
            audioAnalyser.connect(outContext.destination);
        }
        
        const source = outContext.createBufferSource();
        currentAudioSource = source;
        source.buffer = audioBuffer;
        source.connect(audioAnalyser);
        source.onended = () => {
            currentAudioSource = null;
            aiAudioPlayedSecs += aiChunkDuration;
            playNextAudio();
        }; // trigger the next chunk gaplessly!
        source.start(0);
        
        aiChunkStartContextTime = outContext.currentTime;
        aiChunkDuration = audioBuffer.duration;
        if (!progressAnimId) updateProgressBar();
        
        if (!visualizerId) renderVisualizer();
    } catch (err) {
        console.error("Error decoding TTS audio:", err);
        playNextAudio();
    }
}

function updateProgressBar() {
    if (!isPlayingAudio || isPaused) {
        progressAnimId = null;
        return;
    }
    
    progressBar.style.transition = 'none'; // Prevent CSS fighting requestAnimationFrame
    
    let currentChunkProgress = outContext.currentTime - aiChunkStartContextTime;
    if (currentChunkProgress < 0) currentChunkProgress = 0;
    if (currentChunkProgress > aiChunkDuration) currentChunkProgress = aiChunkDuration;
    
    let totalPlayed = aiAudioPlayedSecs + currentChunkProgress;
        let targetPercent = aiAudioTotalSecs > 0 ? 100 - ((totalPlayed / aiAudioTotalSecs) * 100) : 0;
        if (targetPercent < 0) targetPercent = 0;
        if (targetPercent > 100) targetPercent = 100;
        
        // Lerp magic: Move visual percent 10% of the way to the target percent every frame
        currentVisualPercent += (targetPercent - currentVisualPercent) * 0.1;
    
    if (progressLogThrottler++ % 15 === 0) {
            console.log(`[Progress UI] Target: ${targetPercent.toFixed(1)}% | Visual: ${currentVisualPercent.toFixed(1)}%`);
    }

        progressBar.style.width = `${currentVisualPercent}%`;
    
    progressAnimId = requestAnimationFrame(updateProgressBar);
}

function renderInputVisualizer() {
    if (!isDictationActive || !inputAnalyser || isMuted) {
        muteBtn.style.transform = 'scale(1)';
        muteBtn.style.boxShadow = 'none';
        inputVisualizerId = null;
        return;
    }
    
    const dataArray = new Uint8Array(inputAnalyser.frequencyBinCount);
    inputAnalyser.getByteFrequencyData(dataArray);
    
    let sum = 0;
    for (let i = 0; i < dataArray.length; i++) sum += dataArray[i];
    const avg = sum / dataArray.length; // 0 to 255
    
    const scale = 1 + (avg / 255) * 0.15;
    const shadow = (avg / 255) * 40;
    
    muteBtn.style.transform = `scale(${scale})`;
    muteBtn.style.boxShadow = `0 0 ${shadow}px ${shadow/2}px rgba(215, 186, 125, 0.8)`;
    
    inputVisualizerId = requestAnimationFrame(renderInputVisualizer);
}

function renderVisualizer() {
    if (!isPlayingAudio || !audioAnalyser || !isDictationActive) {
        visualizerId = null;
        mainToggleBtn.style.transform = 'scale(1)';
        mainToggleBtn.style.boxShadow = 'none';
        return;
    }
    
    const dataArray = new Uint8Array(audioAnalyser.frequencyBinCount);
    audioAnalyser.getByteFrequencyData(dataArray);
    
    let sum = 0;
    for (let i = 0; i < dataArray.length; i++) sum += dataArray[i];
    const avg = sum / dataArray.length; // 0 to 255
    
    // Map amplitude to scale (1 to 1.15) and glow size
    const scale = 1 + (avg / 255) * 0.15;
    const shadow = (avg / 255) * 60;
    
    mainToggleBtn.style.transform = `scale(${scale})`;
    mainToggleBtn.style.boxShadow = `0 0 ${shadow}px ${shadow/2}px rgba(78, 201, 176, 0.8)`;
    
    visualizerId = requestAnimationFrame(renderVisualizer);
}

function stopAudio() {
    audioQueue = []; // Clear the pending playlist
    if (currentAudioSource) {
        try { currentAudioSource.stop(); } catch (e) {}
        currentAudioSource = null;
        isPlayingAudio = false;
            currentVisualPercent = 0;
            progressBar.style.transition = 'none';
        progressBar.style.width = '0%';
        aiAudioTotalSecs = 0;
        aiAudioPlayedSecs = 0;
    }
    if (outContext && outContext.state === 'suspended') {
        outContext.resume(); // Ensure it isn't locked up for the new response
    }
}

async function startRecording() {
    inContext = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 16000 });
    
    if (inContext.state === 'suspended') {
        await inContext.resume();
    }

    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    input = inContext.createMediaStreamSource(stream);
    
    // Using ScriptProcessor for simplicity in this MVP; AudioWorklet is preferred for production
    processor = inContext.createScriptProcessor(4096, 1, 1);
    
    inputAnalyser = inContext.createAnalyser();
    inputAnalyser.fftSize = 256;
    input.connect(inputAnalyser);
    if (!inputVisualizerId) renderInputVisualizer();

    processor.onaudioprocess = (e) => {
        const inputData = e.inputBuffer.getChannelData(0);
        
        // Calculate volume (RMS)
        let sum = 0;
        for (let i = 0; i < inputData.length; i++) {
            sum += inputData[i] * inputData[i];
        }
        const rms = isMuted ? 0 : Math.sqrt(sum / inputData.length);

        if (rms > NOISE_THRESHOLD) {
            // Speech detected
            if (!isSpeaking) {
                if (outContext && outContext.state === 'running') {
                    outContext.suspend(); // PAUSE audio, don't destroy it yet
                }
                status.innerText = "Status: Recording (Speaking)...";
                isSpeaking = true;
                // Prepend the pre-roll buffer to catch the very start of the word
                audioChunks = [...preRollBuffer];
            }
            silenceFrames = 0;
            audioChunks.push(new Float32Array(inputData));
        } else if (isSpeaking) {
            // Silence detected during a recording
            audioChunks.push(new Float32Array(inputData)); // keep trailing silence
            silenceFrames++;
            
            if (silenceFrames >= SILENCE_FRAMES_LIMIT) {
                // We are done speaking
                isSpeaking = false;
                silenceFrames = 0;
                
                if (audioChunks.length >= MIN_CHUNKS) {
                    status.innerText = "Status: Processing with Whisper...";
                    sendAndClearBuffer();
                } else {
                    // It was just a mic pop or sniff, discard it
                    audioChunks = [];
                    status.innerText = isMuted ? "Status: Connected - Muted" : "Status: Connected - Listening...";
                }
            }
        } else {
            // Not speaking, maintain a rolling buffer of the last few frames
            preRollBuffer.push(new Float32Array(inputData));
            if (preRollBuffer.length > PRE_ROLL_FRAMES) {
                preRollBuffer.shift(); // Remove the oldest frame
            }
        }
    };

    input.connect(processor);
    processor.connect(inContext.destination);
}

function sendAndClearBuffer() {
    if (socket.readyState !== WebSocket.OPEN) {
        audioChunks = []; // Don't hoard memory if socket is dead
        return;
    }
    
    const totalLength = audioChunks.reduce((acc, val) => acc + val.length, 0);
    const pcmData = new Int16Array(totalLength);
    let offset = 0;
    for (const chunk of audioChunks) {
        for (let i = 0; i < chunk.length; i++) {
            pcmData[offset++] = Math.max(-1, Math.min(1, chunk[i])) * 0x7FFF;
        }
    }
    
    socket.send(pcmData.buffer);
    audioChunks = [];
}

function stopRecording() {
    if (input && input.mediaStream) {
        input.mediaStream.getTracks().forEach(track => track.stop());
    }
    if (input) {
        input.disconnect();
        input = null;
    }
    if (processor) {
        processor.disconnect();
        processor = null;
    }
    if (inContext && inContext.state !== 'closed') {
        inContext.close();
        inContext = null;
    }
}
