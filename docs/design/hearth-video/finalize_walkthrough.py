import json
import pathlib
import subprocess

root = pathlib.Path(__file__).resolve().parent
source = root / 'hearth-agent-walkthrough-raw.mp4'
output = root.parent / 'hearth-agent-walkthrough.mp4'
recording = json.loads((root / 'chapters.json').read_text())
probe = json.loads(subprocess.check_output([
    'ffprobe', '-v', 'error', '-show_format', '-show_streams', '-of', 'json', str(source)
], text=True))
duration = float(probe['format']['duration'])
chapters = recording['chapters']
assert len(chapters) == 11, 'The walkthrough did not reach every chapter.'
assert duration > 100, 'Unexpectedly short recording.'
stream = next(s for s in probe['streams'] if s['codec_type'] == 'video')
assert (stream['width'], stream['height']) == (1920, 1080)

metadata = [
    ';FFMETADATA1',
    'title=Hearth — Agent-first experience walkthrough',
    'artist=Hearth',
    'comment=Interactive prototype. Sample data and simulated actions. Silent walkthrough with on-screen chapter titles.',
]
for index, chapter in enumerate(chapters):
    start = int(chapter['start'] * 1000)
    end = int((chapters[index + 1]['start'] if index + 1 < len(chapters) else duration) * 1000)
    metadata.extend(['[CHAPTER]', 'TIMEBASE=1/1000', f'START={start}', f'END={end}', f'title={index + 1:02d} · {chapter["title"]}'])
metadata_path = root / 'chapters.ffmetadata'
metadata_path.write_text('\n'.join(metadata) + '\n')
subprocess.run([
    'ffmpeg', '-hide_banner', '-loglevel', 'error', '-n', '-i', str(source),
    '-i', str(metadata_path), '-map', '0:v:0', '-map_metadata', '1',
    '-map_chapters', '1', '-c:v', 'copy', '-movflags', '+faststart', str(output),
], check=True)

# A chapter contact sheet verifies the actual captured content, not just file existence.
selected_frames = [int((chapter['start'] + 5) * 30) for chapter in chapters]
selection = '+'.join(f'eq(n,{frame})' for frame in selected_frames)
subprocess.run([
    'ffmpeg', '-hide_banner', '-loglevel', 'error', '-i', str(output),
    '-vf', f"select='{selection}',scale=480:-1,tile=3x4:padding=4:margin=4:color=0x111416",
    '-frames:v', '1', '-update', '1', str(root / 'chapter-contact-sheet.jpg'),
], check=True)
subprocess.run([
    'ffmpeg', '-hide_banner', '-loglevel', 'error', '-ss', '3', '-i', str(output),
    '-frames:v', '1', '-update', '1', str(root / 'poster.jpg'),
], check=True)
(root / 'walkthrough.mp4').symlink_to('../hearth-agent-walkthrough.mp4')
print(json.dumps({
    'file': str(output), 'duration_seconds': duration,
    'size_mb': round(output.stat().st_size / 1_000_000, 2),
    'resolution': '1920x1080', 'chapters': len(chapters),
}, indent=2))
