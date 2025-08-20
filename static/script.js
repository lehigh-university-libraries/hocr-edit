let currentSession = null;
let currentImageIndex = 0;
let hocrData = null;
let selectedWordId = null;
let currentWordIndex = -1;
let wordNavigationOrder = [];
let imageScale = 1;
let showLowConfidence = false;

// Drawing mode variables
let drawingMode = false;
let isDrawing = false;
let drawingStart = null;
let currentDrawingBox = null;
let pendingAnnotation = null;

// Load sessions on page load
document.addEventListener('DOMContentLoaded', loadSessions);

// Global keyboard event listener for tab navigation
document.addEventListener('keydown', function(e) {
    // Only handle navigation when correction interface is visible
    if (document.getElementById('correction-section').classList.contains('hidden')) {
        return;
    }

    switch(e.key) {
        case 'Tab':
            e.preventDefault();
            if (e.shiftKey) {
                navigateToPreviousWord();
            } else {
                navigateToNextWord();
            }
            break;
        case 'Enter':
            e.preventDefault();
            if (selectedWordId && document.getElementById('word-text') === document.activeElement) {
                applyWordChanges();
            }
            break;
        case 'Delete':
            if (selectedWordId && document.activeElement !== document.getElementById('word-text')) {
                e.preventDefault();
                deleteSelectedWord();
            }
            break;
        case 'Escape':
            e.preventDefault();
            if (drawingMode) {
                toggleDrawingMode();
            } else {
                clearSelection();
            }
            break;
        case 'd':
        case 'D':
            if (document.activeElement === document.body || 
                document.activeElement === document.getElementById('image-container') ||
                document.activeElement.tagName === 'BUTTON') {
                e.preventDefault();
                toggleDrawingMode();
            }
            break;
    }
});

// Drawing mode functions
function toggleDrawingMode() {
    drawingMode = !drawingMode;
    const btn = document.getElementById('drawing-mode-btn');
    const imageContainer = document.getElementById('image-container');
    
    if (drawingMode) {
        btn.textContent = '❌ Exit Drawing';
        btn.classList.remove('btn-primary');
        btn.classList.add('btn-danger');
        imageContainer.classList.add('drawing-mode');
        clearSelection();
        setupDrawingEvents();
    } else {
        btn.textContent = '✏️ Draw New Word';
        btn.classList.remove('btn-danger');
        btn.classList.add('btn-primary');
        imageContainer.classList.remove('drawing-mode');
        removeDrawingEvents();
        cancelAnnotation();
    }
}

function setupDrawingEvents() {
    const imageContainer = document.getElementById('image-container');
    imageContainer.addEventListener('mousedown', startDrawing);
    imageContainer.addEventListener('mousemove', updateDrawing);
    imageContainer.addEventListener('mouseup', endDrawing);
    imageContainer.addEventListener('mouseleave', cancelDrawing);
}

function removeDrawingEvents() {
    const imageContainer = document.getElementById('image-container');
    imageContainer.removeEventListener('mousedown', startDrawing);
    imageContainer.removeEventListener('mousemove', updateDrawing);
    imageContainer.removeEventListener('mouseup', endDrawing);
    imageContainer.removeEventListener('mouseleave', cancelDrawing);
}

function startDrawing(e) {
    if (!drawingMode) return;
    
    // Prevent scrolling and other default behaviors
    e.preventDefault();
    e.stopPropagation();
    
    isDrawing = true;
    const rect = e.currentTarget.getBoundingClientRect();
    const img = document.getElementById('current-image');
    const imgRect = img.getBoundingClientRect();
    
    // Calculate position relative to image
    drawingStart = {
        x: e.clientX - imgRect.left,
        y: e.clientY - imgRect.top
    };
    
    // Create drawing box
    currentDrawingBox = document.createElement('div');
    currentDrawingBox.className = 'drawing-box';
    currentDrawingBox.style.left = drawingStart.x + 'px';
    currentDrawingBox.style.top = drawingStart.y + 'px';
    currentDrawingBox.style.width = '0px';
    currentDrawingBox.style.height = '0px';
    
    document.getElementById('hocr-overlay').appendChild(currentDrawingBox);
}

function updateDrawing(e) {
    if (!drawingMode || !isDrawing || !currentDrawingBox) return;
    
    // Prevent scrolling and other default behaviors
    e.preventDefault();
    e.stopPropagation();
    
    const img = document.getElementById('current-image');
    const imgRect = img.getBoundingClientRect();
    
    const currentX = e.clientX - imgRect.left;
    const currentY = e.clientY - imgRect.top;
    
    const left = Math.min(drawingStart.x, currentX);
    const top = Math.min(drawingStart.y, currentY);
    const width = Math.abs(currentX - drawingStart.x);
    const height = Math.abs(currentY - drawingStart.y);
    
    currentDrawingBox.style.left = left + 'px';
    currentDrawingBox.style.top = top + 'px';
    currentDrawingBox.style.width = width + 'px';
    currentDrawingBox.style.height = height + 'px';
}

function endDrawing(e) {
    if (!drawingMode || !isDrawing || !currentDrawingBox) return;
    
    isDrawing = false;
    
    // Check if the drawn box is large enough
    const width = parseFloat(currentDrawingBox.style.width);
    const height = parseFloat(currentDrawingBox.style.height);
    
    if (width < 10 || height < 10) {
        // Too small, cancel
        cancelDrawing();
        return;
    }
    
    // Store the bounding box coordinates
    const img = document.getElementById('current-image');
    const scaleX = img.naturalWidth / img.clientWidth;
    const scaleY = img.naturalHeight / img.clientHeight;
    
    const left = parseFloat(currentDrawingBox.style.left);
    const top = parseFloat(currentDrawingBox.style.top);
    
    pendingAnnotation = {
        bbox: [
            Math.round(left * scaleX),
            Math.round(top * scaleY),
            Math.round((left + width) * scaleX),
            Math.round((top + height) * scaleY)
        ],
        element: currentDrawingBox
    };
    
    // Show annotation modal
    showAnnotationModal();
}

function cancelDrawing() {
    if (currentDrawingBox) {
        currentDrawingBox.remove();
        currentDrawingBox = null;
    }
    isDrawing = false;
    drawingStart = null;
}

function showAnnotationModal() {
    document.getElementById('annotation-modal').classList.remove('hidden');
    const input = document.getElementById('annotation-text');
    input.value = '';
    input.focus();
    
    // Handle Enter key in modal
    input.onkeydown = function(e) {
        if (e.key === 'Enter') {
            e.preventDefault();
            saveAnnotation();
        } else if (e.key === 'Escape') {
            e.preventDefault();
            cancelAnnotation();
        }
    };
}

function saveAnnotation() {
    const text = document.getElementById('annotation-text').value.trim();
    if (!text || !pendingAnnotation) {
        cancelAnnotation();
        return;
    }
    
    // Generate new word ID
    const newWordId = 'word_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
    
    // Determine line ID (find the closest existing line or create new one)
    let lineId = 'line_new_' + Date.now();
    if (hocrData && hocrData.words.length > 0) {
        // Find the closest line based on Y coordinate
        const bbox = pendingAnnotation.bbox;
        const centerY = (bbox[1] + bbox[3]) / 2;
        
        let closestLine = null;
        let minDistance = Infinity;
        
        hocrData.words.forEach(word => {
            const wordCenterY = (word.bbox[1] + word.bbox[3]) / 2;
            const distance = Math.abs(centerY - wordCenterY);
            if (distance < minDistance && distance < 50) { // Within 50 pixels
                minDistance = distance;
                closestLine = word.line_id;
            }
        });
        
        if (closestLine) {
            lineId = closestLine;
        }
    }
    
    // Create new word object
    const newWord = {
        id: newWordId,
        text: text,
        bbox: pendingAnnotation.bbox,
        confidence: 95, // High confidence for manually added words
        line_id: lineId
    };
    
    // Add to hocrData
    if (!hocrData) {
        hocrData = { words: [] };
    }
    hocrData.words.push(newWord);
    
    // Sort words by reading order to maintain proper indexing
    hocrData.words.sort((a, b) => {
        const yDiff = a.bbox[1] - b.bbox[1];
        if (Math.abs(yDiff) > 10) {
            return yDiff;
        }
        return a.bbox[0] - b.bbox[0];
    });
    
    // Remove the drawing box since we'll recreate it properly
    if (pendingAnnotation.element) {
        pendingAnnotation.element.remove();
    }
    
    // Re-render the entire overlay to ensure proper indexing and event handlers
    renderHOCROverlay();
    
    // Update hOCR source and metrics
    updateHOCRSource();
    updateMetrics();
    updateWordCounter();
    
    // Close modal and reset
    closeAnnotationModal();
    
    // Find the index of the newly added word and select it
    setTimeout(() => {
        const newWordIndex = hocrData.words.findIndex(w => w.id === newWordId);
        if (newWordIndex !== -1) {
            currentWordIndex = newWordIndex;
            selectWord(newWordId);
        }
    }, 100);
}

function cancelAnnotation() {
    if (pendingAnnotation && pendingAnnotation.element) {
        pendingAnnotation.element.remove();
    }
    pendingAnnotation = null;
    closeAnnotationModal();
    cancelDrawing();
}

function closeAnnotationModal() {
    document.getElementById('annotation-modal').classList.add('hidden');
    document.getElementById('annotation-text').onkeydown = null;
}

function navigateToNextWord() {
    if (!hocrData || !hocrData.words || hocrData.words.length === 0) return;
    
    currentWordIndex = (currentWordIndex + 1) % hocrData.words.length;
    selectWordByIndex(currentWordIndex);
    scrollWordIntoView();
}

function navigateToPreviousWord() {
    if (!hocrData || !hocrData.words || hocrData.words.length === 0) return;
    
    currentWordIndex = currentWordIndex <= 0 ? hocrData.words.length - 1 : currentWordIndex - 1;
    selectWordByIndex(currentWordIndex);
    scrollWordIntoView();
}

function selectWordByIndex(index) {
    if (!hocrData || !hocrData.words[index]) return;
    
    const word = hocrData.words[index];
    selectWord(word.id);
    updateWordCounter();
    
    // Focus the word input for immediate editing
    setTimeout(() => {
        const wordInput = document.getElementById('word-text');
        if (wordInput) {
            wordInput.focus();
            wordInput.select();
        }
    }, 100);
}

function scrollWordIntoView() {
    if (!selectedWordId) return;
    
    const wordBox = document.getElementById('box-' + selectedWordId);
    if (wordBox) {
        const imageContainer = document.getElementById('image-container');
        const containerRect = imageContainer.getBoundingClientRect();
        const wordRect = wordBox.getBoundingClientRect();
        
        // Check if word is outside visible area
        if (wordRect.top < containerRect.top || wordRect.bottom > containerRect.bottom ||
            wordRect.left < containerRect.left || wordRect.right > containerRect.right) {
            
            // Calculate scroll position to center the word
            const scrollTop = imageContainer.scrollTop + wordRect.top - containerRect.top - (containerRect.height / 2);
            const scrollLeft = imageContainer.scrollLeft + wordRect.left - containerRect.left - (containerRect.width / 2);
            
            imageContainer.scrollTo({
                top: Math.max(0, scrollTop),
                left: Math.max(0, scrollLeft),
                behavior: 'smooth'
            });
        }
    }
}

function updateWordCounter() {
    const counter = document.getElementById('word-counter');
    if (hocrData && hocrData.words) {
        counter.textContent = `Word ${currentWordIndex + 1} of ${hocrData.words.length}`;
    } else {
        counter.textContent = 'Word 0 of 0';
    }
}

function clearSelection() {
    selectedWordId = null;
    currentWordIndex = -1;
    
    document.querySelectorAll('.hocr-word-box').forEach(box => {
        box.classList.remove('selected');
    });
    
    document.getElementById('word-editor').style.display = 'none';
    document.getElementById('no-selection').style.display = 'block';
    updateWordCounter();
}

function applyWordChanges() {
    if (selectedWordId) {
        updateSelectedWord();
        // Auto-advance to next word after applying changes
        setTimeout(() => navigateToNextWord(), 100);
    }
}

async function loadSessions() {
    try {
        const response = await fetch('/api/sessions');
        const sessions = await response.json();
        displaySessions(sessions);
    } catch (error) {
        console.error('Error loading sessions:', error);
    }
}

function displaySessions(sessions) {
    const container = document.getElementById('sessions-list');
    if (sessions.length === 0) {
        container.innerHTML = '<p>No sessions found. Upload images to get started.</p>';
        return;
    }

    const html = sessions.map(session => 
        `<div style="border: 1px solid #333; padding: 15px; margin: 10px 0; border-radius: 8px; background: #111;">
        <h4>Session: ${session.id}</h4>
        <p>Images: ${session.images.length} | Completed: ${session.images.filter(img => img.completed).length}</p>
        <p>Created: ${new Date(session.created_at).toLocaleString()}</p>
        <button class="btn btn-primary" onclick="loadSession('${session.id}')">Continue</button>
        </div>`
    ).join('');
    container.innerHTML = html;
}

async function handleUpload() {
    const fileInput = document.getElementById('file-input');
    const files = fileInput.files;
    if (files.length === 0) {
        alert('Please select files');
        return;
    }

    // Show upload progress
    const uploadArea = document.getElementById('upload-area');
    uploadArea.innerHTML = '<h3>Processing files...</h3><p>Please wait while files are uploaded and processed with OCR.</p>';

    const formData = new FormData();
    for (let file of files) {
        formData.append('files', file);
    }

    try {
        const response = await fetch('/api/upload', {
            method: 'POST',
            body: formData
        });
        
        const result = await response.json();
        
        if (!response.ok) {
            throw new Error(result.error || 'Upload failed');
        }
        
        if (result.session_id) {
            console.log('Upload successful:', result.message);
            loadSession(result.session_id);
        } else {
            throw new Error('No session ID received');
        }
    } catch (error) {
        console.error('Upload error:', error);
        alert('Upload failed: ' + error.message);
        
        // Reset upload area
        uploadArea.innerHTML = `
            <h3>Start New hOCR Correction Session</h3>
            <p>Upload images - they'll be processed with hOCR-capable OCR</p>
            <input type="file" id="file-input" accept=".jpg,.jpeg,.png,.gif,.csv" multiple style="margin: 20px 0;">
            <br>
            <button class="btn btn-primary" onclick="handleUpload()">Upload & Process</button>
        `;
    }
}

async function loadSession(sessionId) {
    try {
        const response = await fetch('/api/sessions/' + sessionId);
        currentSession = await response.json();
        currentImageIndex = currentSession.current || 0;
        showCorrectionInterface();
        loadCurrentImage();
    } catch (error) {
        console.error('Error loading session:', error);
    }
}

function showCorrectionInterface() {
    document.getElementById('upload-section').classList.add('hidden');
    document.getElementById('correction-section').classList.remove('hidden');
}

async function loadCurrentImage() {
    if (!currentSession || currentImageIndex >= currentSession.images.length) {
        finishSession();
        return;
    }

    // Disable drawing mode when switching images
    if (drawingMode) {
        toggleDrawingMode();
    }

    const image = currentSession.images[currentImageIndex];
    const img = document.getElementById('current-image');
    
    img.onload = function() {
        // Parse hOCR and create overlay
        parseAndDisplayHOCR(image.corrected_hocr || image.original_hocr);
        updateProgress();
        updateMetrics();
        
        // Reset navigation state for new image
        currentWordIndex = -1;
        selectedWordId = null;
        clearSelection();
    };
    
    img.src = image.image_url || '/static/uploads/' + image.image_path;
    document.getElementById('hocr-editor').value = image.corrected_hocr || image.original_hocr;
}

async function parseAndDisplayHOCR(hocrXML) {
    try {
        const response = await fetch('/api/hocr/parse', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ hocr: hocrXML })
        });
        hocrData = await response.json();
        
        // Sort words by reading order (top to bottom, left to right)
        if (hocrData && hocrData.words) {
            hocrData.words.sort((a, b) => {
                // Sort by Y position first (top to bottom)
                const yDiff = a.bbox[1] - b.bbox[1];
                if (Math.abs(yDiff) > 10) { // Allow some tolerance for same line
                    return yDiff;
                }
                // If roughly same Y, sort by X position (left to right)
                return a.bbox[0] - b.bbox[0];
            });
            
            // Ensure all words have proper array-format bboxes
            hocrData.words.forEach(word => {
                if (word.bbox && typeof word.bbox === 'object' && !Array.isArray(word.bbox)) {
                    // Convert object format {x1, y1, x2, y2} to array format [x1, y1, x2, y2]
                    word.bbox = [word.bbox.x1 || word.bbox[0], word.bbox.y1 || word.bbox[1], 
                                word.bbox.x2 || word.bbox[2], word.bbox.y2 || word.bbox[3]];
                }
            });
        }
        
        renderHOCROverlay();
        updateWordCounter();
    } catch (error) {
        console.error('Error parsing hOCR:', error);
    }
}

function renderHOCROverlay() {
    const overlay = document.getElementById('hocr-overlay');
    const img = document.getElementById('current-image');
    
    overlay.innerHTML = '';
    
    if (!hocrData || !hocrData.words) return;

    // Wait for image to load to get dimensions
    if (img.naturalWidth === 0) {
        img.onload = renderHOCROverlay;
        return;
    }

    const scaleX = img.clientWidth / img.naturalWidth;
    const scaleY = img.clientHeight / img.naturalHeight;

    hocrData.words.forEach((word, index) => {
        const wordBox = document.createElement('div');
        
        // Check if this is a manually added word (preserve existing element if it exists)
        const existingBox = document.getElementById('box-' + word.id);
        if (existingBox && existingBox.classList.contains('new-word-box')) {
            // Update the existing manually added word box
            wordBox.className = existingBox.className;
            existingBox.remove();
        } else {
            // Regular OCR detected word
            wordBox.className = 'hocr-word-box';
        }
        
        wordBox.id = 'box-' + word.id;
        wordBox.setAttribute('data-word-index', index);
        
        // Apply confidence-based styling for OCR words
        if (word.confidence < 60 && !wordBox.classList.contains('new-word-box')) {
            wordBox.classList.add('low-confidence');
        }
        
        // Scale bounding box to image display size
        const x1 = word.bbox[0];
        const y1 = word.bbox[1];
        const x2 = word.bbox[2];
        const y2 = word.bbox[3];
        wordBox.style.left = (x1 * scaleX) + 'px';
        wordBox.style.top = (y1 * scaleY) + 'px';
        wordBox.style.width = ((x2 - x1) * scaleX) + 'px';
        wordBox.style.height = ((y2 - y1) * scaleY) + 'px';
        
        wordBox.title = word.text + ' (conf: ' + word.confidence + ')';
        wordBox.onclick = () => {
            if (!drawingMode) {
                currentWordIndex = index;
                selectWord(word.id);
            }
        };
        
        overlay.appendChild(wordBox);
    });
}

function selectWord(wordId) {
    // Clear previous selection
    document.querySelectorAll('.hocr-word-box').forEach(box => {
        box.classList.remove('selected');
    });
    
    // Select new word
    const wordBox = document.getElementById('box-' + wordId);
    if (wordBox) {
        wordBox.classList.add('selected');
        selectedWordId = wordId;
        
        // Update currentWordIndex based on selected word
        const wordIndex = parseInt(wordBox.getAttribute('data-word-index'));
        if (!isNaN(wordIndex)) {
            currentWordIndex = wordIndex;
        }
        
        const word = hocrData.words.find(w => w.id === wordId);
        if (word) {
            showWordEditor(word);
        }
        
        updateWordCounter();
    }
}

function showWordEditor(word) {
    document.getElementById('word-editor').style.display = 'block';
    document.getElementById('no-selection').style.display = 'none';
    
    document.getElementById('word-id').textContent = word.id;
    document.getElementById('word-text').value = word.text;
    document.getElementById('word-confidence').innerHTML = getConfidenceHTML(word.confidence);
    document.getElementById('word-bbox').textContent = word.bbox.join(', ');
}

function getConfidenceHTML(conf) {
    let className = 'conf-high';
    if (conf < 60) className = 'conf-low';
    else if (conf < 80) className = 'conf-medium';
    
    return `<span class="confidence-indicator ${className}">${conf}%</span>`;
}

function updateSelectedWord() {
    if (!selectedWordId || !hocrData) return;
    
    const newText = document.getElementById('word-text').value;
    const word = hocrData.words.find(w => w.id === selectedWordId);
    
    if (word) {
        word.text = newText;
        updateHOCRSource();
        updateMetrics();
    }
}

function deleteSelectedWord() {
    if (!selectedWordId || !hocrData) return;
    
    if (confirm('Delete this word?')) {
        const wordIndex = hocrData.words.findIndex(w => w.id === selectedWordId);
        hocrData.words = hocrData.words.filter(w => w.id !== selectedWordId);
        
        // Adjust currentWordIndex if necessary
        if (wordIndex <= currentWordIndex && currentWordIndex > 0) {
            currentWordIndex--;
        }
        
        selectedWordId = null;
        
        document.getElementById('word-editor').style.display = 'none';
        document.getElementById('no-selection').style.display = 'block';
        
        updateHOCRSource();
        renderHOCROverlay();
        updateMetrics();
        updateWordCounter();
    }
}

function updateHOCRSource() {
    if (!hocrData) return;
    
    // Generate hOCR XML from current data
    const hocr = generateHOCRXML(hocrData);
    document.getElementById('hocr-editor').value = hocr;
    
    // Update session data
    if (currentSession && currentSession.images[currentImageIndex]) {
        currentSession.images[currentImageIndex].corrected_hocr = hocr;
    }
}

function updateHOCRFromSource() {
    const hocrXML = document.getElementById('hocr-editor').value;
    parseAndDisplayHOCR(hocrXML);
    
    // Update session data
    if (currentSession && currentSession.images[currentImageIndex]) {
        currentSession.images[currentImageIndex].corrected_hocr = hocrXML;
    }
}

function generateHOCRXML(data) {
    // Basic hOCR XML generation
    let xml = '<?xml version="1.0" encoding="UTF-8"?>\n';
    xml += '<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">\n';
    xml += '<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">\n';
    xml += '<head>\n<title></title>\n</head>\n<body>\n';
    
    xml += '<div class="ocr_page" id="page_1" title="bbox 0 0 ' + (currentSession.images[currentImageIndex].image_width || 1000) + ' ' + (currentSession.images[currentImageIndex].image_height || 1000) + '">\n';
    
    // Group words by line, sorting them first
    const sortedWords = [...data.words].sort((a, b) => {
        // Sort by Y position first (top to bottom)
        const yDiff = a.bbox[1] - b.bbox[1];
        if (Math.abs(yDiff) > 10) { // Allow some tolerance for same line
            return yDiff;
        }
        // If roughly same Y, sort by X position (left to right)
        return a.bbox[0] - b.bbox[0];
    });
    
    const lineGroups = {};
    sortedWords.forEach(word => {
        if (!lineGroups[word.line_id]) {
            lineGroups[word.line_id] = [];
        }
        lineGroups[word.line_id].push(word);
    });
    
    // Generate XML for each line
    Object.keys(lineGroups).forEach(lineId => {
        const words = lineGroups[lineId];
        if (words.length === 0) return;
        
        // Sort words within line by X position
        words.sort((a, b) => a.bbox[0] - b.bbox[0]);
        
        // Calculate line bbox
        const lineBbox = words.reduce((bbox, word) => {
            return [
                Math.min(bbox[0], word.bbox[0]),
                Math.min(bbox[1], word.bbox[1]),
                Math.max(bbox[2], word.bbox[2]),
                Math.max(bbox[3], word.bbox[3])
            ];
        }, [Infinity, Infinity, -Infinity, -Infinity]);
        
        xml += '  <span class="ocr_line" id="' + lineId + '" title="bbox ' + lineBbox.join(' ') + '">\n';
        
        words.forEach(word => {
            xml += '    <span class="ocrx_word" id="' + word.id + '" title="bbox ' + word.bbox.join(' ') + '; x_wconf ' + word.confidence + '">' + escapeXML(word.text) + '</span>\n';
        });
        
        xml += '  </span>\n';
    });
    
    xml += '</div>\n</body>\n</html>';
    return xml;
}

function escapeXML(text) {
    return text.replace(/&/g, '&amp;')
                  .replace(/</g, '&lt;')
                  .replace(/>/g, '&gt;')
                  .replace(/"/g, '&quot;')
                  .replace(/'/g, '&#39;');
}

function toggleLowConfidence() {
    showLowConfidence = !showLowConfidence;
    document.querySelectorAll('.hocr-word-box').forEach(box => {
        if (showLowConfidence) {
            box.style.display = box.classList.contains('low-confidence') ? 'block' : 'none';
        } else {
            box.style.display = 'block';
        }
    });
}

function updateProgress() {
    const total = currentSession.images.length;
    const current = currentImageIndex + 1;
    const percentage = (current / total) * 100;
    
    document.getElementById('progress-text').textContent = `Image ${current} of ${total}`;
    document.getElementById('progress-bar').style.width = percentage + '%';
}

async function updateMetrics() {
    if (!hocrData || !currentSession) return;
    
    const originalText = extractTextFromHOCR(currentSession.images[currentImageIndex].original_hocr);
    const correctedText = extractTextFromHOCR(document.getElementById('hocr-editor').value);
    
    try {
        const response = await fetch('/api/sessions/' + currentSession.id + '/metrics', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                original: originalText,
                corrected: correctedText
            })
        });
        const metrics = await response.json();
        
        document.getElementById('char-similarity').textContent = metrics.character_similarity.toFixed(3);
        document.getElementById('word-accuracy').textContent = metrics.word_accuracy.toFixed(3);
        document.getElementById('word-error-rate').textContent = metrics.word_error_rate.toFixed(3);
        document.getElementById('total-words').textContent = hocrData.words.length;
        
        // Calculate confidence metrics
        const confidences = hocrData.words.map(w => w.confidence);
        const avgConf = confidences.reduce((a, b) => a + b, 0) / confidences.length;
        const lowConfCount = confidences.filter(c => c < 60).length;
        
        document.getElementById('avg-confidence').textContent = Math.round(avgConf) + '%';
        document.getElementById('low-conf-words').textContent = lowConfCount;
        
    } catch (error) {
        console.error('Error calculating metrics:', error);
    }
}

function extractTextFromHOCR(hocrXML) {
    // Simple text extraction from hOCR
    try {
        const parser = new DOMParser();
        const doc = parser.parseFromString(hocrXML, 'text/html');
        const words = doc.querySelectorAll('.ocrx_word');
        return Array.from(words).map(word => word.textContent).join(' ');
    } catch (error) {
        return '';
    }
}

// Zoom controls
function zoomIn() {
    imageScale *= 1.2;
    applyZoom();
}

function zoomOut() {
    imageScale /= 1.2;
    applyZoom();
}

function resetZoom() {
    imageScale = 1;
    applyZoom();
}

function applyZoom() {
    const img = document.getElementById('current-image');
    const overlay = document.getElementById('hocr-overlay');
    
    img.style.transform = `scale(${imageScale})`;
    overlay.style.transform = `scale(${imageScale})`;
    
    // Re-render overlay with new scale
    setTimeout(renderHOCROverlay, 10);
}

async function saveAndNext() {
    const hocrXML = document.getElementById('hocr-editor').value;
    currentSession.images[currentImageIndex].corrected_hocr = hocrXML;
    currentSession.images[currentImageIndex].completed = true;
    
    // Save to backend
    await saveSession();
    
    currentImageIndex++;
    selectedWordId = null;
    currentWordIndex = -1;
    document.getElementById('word-editor').style.display = 'none';
    document.getElementById('no-selection').style.display = 'block';
    
    loadCurrentImage();
}

function previousImage() {
    if (currentImageIndex > 0) {
        currentImageIndex--;
        selectedWordId = null;
        currentWordIndex = -1;
        document.getElementById('word-editor').style.display = 'none';
        document.getElementById('no-selection').style.display = 'block';
        loadCurrentImage();
    }
}

async function saveSession() {
    try {
        await fetch('/api/sessions/' + currentSession.id, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(currentSession)
        });
    } catch (error) {
        console.error('Error saving session:', error);
    }
}

async function finishSession() {
    await saveSession();
    alert('Session completed! hOCR corrections have been saved.');
    location.reload();
}

// Handle image resize for overlay repositioning
window.addEventListener('resize', () => {
    setTimeout(renderHOCROverlay, 100);
});