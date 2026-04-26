interface WindowProgressProps {
  totalEpisodes: number;
  windowStart: number;   // 1-indexed, first buffered ep
  bufferSize: number;
  season: number;
}

export function WindowProgress({ totalEpisodes, windowStart, bufferSize, season }: WindowProgressProps) {
  if (totalEpisodes === 0) return null;

  const windowEnd = Math.min(windowStart + bufferSize - 1, totalEpisodes);

  return (
    <div className="space-y-1">
      <div className="flex gap-[2px] h-3">
        {Array.from({ length: totalEpisodes }, (_, i) => {
          const ep = i + 1;
          const isWatched  = ep < windowStart;
          const isBuffered = ep >= windowStart && ep <= windowEnd;

          return (
            <div
              key={ep}
              title={`S${String(season).padStart(2,'0')}E${String(ep).padStart(2,'0')} · ${
                isWatched ? 'Watched' : isBuffered ? 'Buffered' : 'Upcoming'
              }`}
              className="flex-1 rounded-[1px] transition-all duration-300"
              style={{
                background: isWatched
                  ? 'rgba(255,255,255,0.08)'
                  : isBuffered
                  ? 'var(--emerald)'
                  : 'rgba(255,255,255,0.04)',
                boxShadow: isBuffered ? '0 0 4px rgba(16,185,129,0.5)' : undefined,
              }}
            />
          );
        })}
      </div>
      <div className="flex justify-between items-center">
        <span className="mono text-[10px] text-white/30">E1</span>
        <span className="mono text-[10px]" style={{ color: 'var(--emerald)' }}>
          E{windowStart}–E{windowEnd} buffered
        </span>
        <span className="mono text-[10px] text-white/30">E{totalEpisodes}</span>
      </div>
    </div>
  );
}
