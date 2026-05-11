'use client';

import { useRef, useCallback } from 'react';
import dynamic from 'next/dynamic';
import { Copy, Download, Save, Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import { toast } from 'sonner';

const MonacoEditor = dynamic(() => import('@monaco-editor/react').then((m) => m.default), {
  ssr: false,
  loading: () => (
    <div className="flex items-center justify-center h-full bg-[#1e1e1e]">
      <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
    </div>
  ),
});

interface YamlEditorProps {
  value: string;
  onChange?: (value: string) => void;
  readOnly?: boolean;
  onSave?: (value: string) => void;
  saving?: boolean;
  height?: string;
  className?: string;
}

export function YamlEditor({
  value,
  onChange,
  readOnly = false,
  onSave,
  saving,
  height = '100%',
  className,
}: YamlEditorProps) {
  const editorRef = useRef<unknown>(null);

  const handleEditorDidMount = useCallback((editor: unknown) => {
    editorRef.current = editor;
  }, []);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(value);
      toast.success('Copied to clipboard');
    } catch {
      toast.error('Failed to copy');
    }
  }, [value]);

  const handleDownload = useCallback(() => {
    const blob = new Blob([value], { type: 'text/yaml' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'resource.yaml';
    a.click();
    URL.revokeObjectURL(url);
  }, [value]);

  const handleSave = useCallback(() => {
    if (onSave) onSave(value);
  }, [onSave, value]);

  return (
    <div className={cn('flex flex-col', className)}>
      {/* Toolbar */}
      <div className="flex items-center justify-between px-3 py-1.5 bg-[#252526] border-b border-[#3c3c3c]">
        <span className="text-xs text-[#cccccc]/60">YAML</span>
        <div className="flex items-center gap-1">
          <button onClick={handleCopy} className="p-1.5 rounded hover:bg-[#3c3c3c] text-[#cccccc]/80 hover:text-[#cccccc] transition-colors" title="Copy">
            <Copy className="h-3.5 w-3.5" />
          </button>
          <button onClick={handleDownload} className="p-1.5 rounded hover:bg-[#3c3c3c] text-[#cccccc]/80 hover:text-[#cccccc] transition-colors" title="Download">
            <Download className="h-3.5 w-3.5" />
          </button>
          {!readOnly && onSave && (
            <button
              onClick={handleSave}
              disabled={saving}
              className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded text-xs font-medium
                bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-50 transition-colors ml-1"
            >
              {saving ? <Loader2 className="h-3 w-3 animate-spin" /> : <Save className="h-3 w-3" />}
              Save
            </button>
          )}
        </div>
      </div>

      {/* Editor */}
      <div className="flex-1 min-h-0" style={{ height }}>
        <MonacoEditor
          language="yaml"
          theme="vs-dark"
          value={value}
          onChange={(v) => onChange?.(v || '')}
          onMount={handleEditorDidMount}
          options={{
            readOnly,
            minimap: { enabled: false },
            fontSize: 13,
            lineNumbers: 'on',
            scrollBeyondLastLine: false,
            wordWrap: 'on',
            tabSize: 2,
            automaticLayout: true,
            renderLineHighlight: 'line',
            folding: true,
            padding: { top: 8 },
          }}
        />
      </div>
    </div>
  );
}
