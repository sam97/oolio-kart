"""A toy animation of the goroutines in the bucket scanner.

Three sources, three encode workers, four buckets and two count workers, so
the shape of the work is visible; the real build picks these from its memory
budget. Please see ARCHITECTURE.md, "How the valid set is built".

    manim -qm scanner_goroutines.py ScannerGoroutines
"""

from manim import *

MONO = "Consolas"

# One colour per bucket, so a code's bucket is visible as it moves.
BUCKET_COLOURS = [BLUE_C, GREEN_C, GOLD_C, PURPLE_B]

READER_COLOUR = TEAL_C
ENCODER_COLOUR = ORANGE
COUNTER_COLOUR = PINK
CHANNEL_COLOUR = GREY_B

# Toy chunks: (source, lines). Each code's bucket is fixed, as
# splitmix64(code) % P would be: every copy lands in the same bucket number.
BUCKET_OF = {
    "HAPPYHRS": 0,
    "GNULINUX": 0,
    "SUPER100": 1,
    "XK4P2QZ9": 1,
    "M3XV8KQA": 1,
    "FIFTYOFF": 2,
    "OVER9000": 2,
    "MOODYHRS": 3,
    "Q7RT3MBN": 3,
    "ZP9W2LTE": 3,
}
# Source 0 is one chunk long, so its reader finishes first and hands its
# semaphore slot to source 2's reader. SUPER100 repeats within source 0, so
# it counts once and is not valid. SHORT and BIRTH are not codes.
CHUNKS = [
    (0, ["HAPPYHRS", "SUPER100", "FIFTYOFF", "SUPER100", "SHORT"]),
    (1, ["MOODYHRS", "GNULINUX", "XK4P2QZ9"]),
    (2, ["HAPPYHRS", "OVER9000", "Q7RT3MBN"]),
    (1, ["FIFTYOFF", "ZP9W2LTE", "BIRTH"]),
    (2, ["GNULINUX", "FIFTYOFF"]),
    (1, ["OVER9000", "M3XV8KQA"]),
]


def label(text, size=20, colour=WHITE):
    return Text(text, font=MONO, font_size=size, color=colour)


def goroutine_box(text, colour, width=1.9, height=0.6):
    box = RoundedRectangle(corner_radius=0.12, width=width, height=height, color=colour)
    box.set_fill(colour, opacity=0.15)
    return VGroup(box, label(text, 16).move_to(box))


def channel(start, end, text):
    pipe = Line(start, end, color=CHANNEL_COLOUR, stroke_width=10, stroke_opacity=0.35)
    arrow = Arrow(start, end, buff=0, color=CHANNEL_COLOUR, stroke_width=2, max_tip_length_to_length_ratio=0.08)
    name = label(text, 14, CHANNEL_COLOUR).next_to(pipe, UP, buff=0.08)
    return VGroup(pipe, arrow, name)


def chunk_square(lines):
    """A pooled buffer holding a chunk of whole lines."""
    rows = VGroup(*[label(line, 11) for line in lines]).arrange(DOWN, buff=0.04, aligned_edge=LEFT)
    box = SurroundingRectangle(rows, buff=0.07, color=WHITE, stroke_width=1.5)
    box.set_fill(GREY_E, opacity=0.9)
    return VGroup(box, rows)


def empty_buffer():
    square = Square(0.22, color=WHITE, stroke_width=1.5)
    square.set_fill(GREY_E, opacity=0.9)
    return square


def bucket_grid(sources=3, buckets=4):
    """Bucket files, one row per source and one column per bucket."""
    cells = VGroup()
    for source in range(sources):
        for bucket in range(buckets):
            cell = Rectangle(width=0.85, height=0.55, color=BUCKET_COLOURS[bucket], stroke_width=2)
            cell.set_fill(BUCKET_COLOURS[bucket], opacity=0.08)
            cells.add(cell)
    cells.arrange_in_grid(rows=sources, cols=buckets, buff=0.08)
    headers = VGroup(*[
        label(f"b{bucket:03d}", 12, BUCKET_COLOURS[bucket]).next_to(cells[bucket], UP, buff=0.08)
        for bucket in range(buckets)
    ])
    rows = VGroup(*[
        label(f"src{source}", 12, GREY_B).next_to(cells[source * buckets], LEFT, buff=0.1)
        for source in range(sources)
    ])
    return cells, VGroup(cells, headers, rows)


class ScannerGoroutines(Scene):
    def construct(self):
        self.title_card()
        self.build_layout()
        self.scatter_phase()
        self.barrier()
        self.count_phase()
        self.ending_card()

    # 1. Title

    def title_card(self):
        title = Text("The bucket scanner's goroutines", font_size=40)
        subtitle = label("scatter  →  wg.Wait()  →  count", 22, GREY_B).next_to(title, DOWN)
        self.play(Write(title), FadeIn(subtitle, shift=UP * 0.2))
        self.wait(1)
        self.play(FadeOut(title), FadeOut(subtitle))

    # 2. Layout for the scatter phase

    def build_layout(self):
        self.phase = label("Phase 1 · scatter", 24, YELLOW).to_corner(UL)

        self.sources = VGroup(*[
            VGroup(
                Rectangle(width=1.5, height=0.55, color=GREY_B).set_fill(GREY_D, 0.4),
                label(f"couponbase{index + 1}", 13),
            )
            for index in range(3)
        ]).arrange(DOWN, buff=0.55).move_to(LEFT * 5.9 + DOWN * 0.2)
        for source in self.sources:
            source[1].move_to(source[0])

        self.readers = VGroup(*[
            goroutine_box("go readSource", READER_COLOUR, width=1.9).next_to(self.sources[index], RIGHT, buff=0.35)
            for index in range(3)
        ])
        reader_note = label("one per file", 12, READER_COLOUR).next_to(self.readers, DOWN, buff=0.38)

        # The semaphore: at most one reader per core runs at once. Two slots
        # here, so the third reader waits its turn.
        self.semaphore_slots = VGroup(*[
            Circle(0.1, color=READER_COLOUR, stroke_width=2) for _ in range(2)
        ]).arrange(RIGHT, buff=0.12)
        semaphore_name = label("reading (cap 2)", 12, READER_COLOUR)
        self.semaphore = VGroup(semaphore_name, self.semaphore_slots).arrange(RIGHT, buff=0.15)
        self.semaphore.next_to(self.readers, UP, buff=0.25)

        # The buffer pool bounds the input in flight.
        self.pool_buffers = VGroup(*[empty_buffer() for _ in range(6)]).arrange(RIGHT, buff=0.08)
        pool_name = label("pool", 12, GREY_B).next_to(self.pool_buffers, LEFT, buff=0.12)
        self.pool = VGroup(pool_name, self.pool_buffers).next_to(self.semaphore, UP, buff=0.3)

        chunks_start = self.readers.get_right() + RIGHT * 0.25
        self.chunks_channel = channel(chunks_start, chunks_start + RIGHT * 1.6, "chunks")

        self.encoders = VGroup(*[
            goroutine_box(f"encode worker {index}", ENCODER_COLOUR, width=2.1)
            for index in range(3)
        ]).arrange(DOWN, buff=0.55).next_to(self.chunks_channel, RIGHT, buff=0.25)

        self.cells, self.grid = bucket_grid()
        self.grid.next_to(self.encoders, RIGHT, buff=0.7)
        hash_note = label("splitmix64(code) % 4", 13, GREY_B).next_to(self.grid, DOWN, buff=0.2)
        self.grid.add(hash_note)

        self.play(FadeIn(self.phase))
        self.play(LaggedStart(*[FadeIn(source, shift=RIGHT * 0.2) for source in self.sources], lag_ratio=0.2))
        self.play(
            LaggedStart(*[GrowFromCenter(reader) for reader in self.readers], lag_ratio=0.2),
            FadeIn(reader_note),
        )
        self.play(FadeIn(self.semaphore), FadeIn(self.pool))
        self.play(Create(self.chunks_channel))
        self.play(LaggedStart(*[GrowFromCenter(encoder) for encoder in self.encoders], lag_ratio=0.2))
        self.play(FadeIn(self.grid))
        self.wait(0.5)

    # 3. Read and scatter

    def acquire(self, reader, slot):
        token = Dot(radius=0.07, color=READER_COLOUR).move_to(self.semaphore_slots[slot])
        return token, [self.readers[reader][0].animate.set_fill(READER_COLOUR, 0.5), FadeIn(token, scale=0.5)]

    def read_chunk(self, chunk_index):
        """A reader takes a pooled buffer, fills it with lines and sends it."""
        source, lines = CHUNKS[chunk_index]
        reader = self.readers[source]
        buffer = self.pool_buffers[chunk_index % len(self.pool_buffers)]
        chunk = chunk_square(lines).move_to(reader)
        self.play(buffer.animate.move_to(reader).scale(0.8), run_time=0.4)
        self.play(ReplacementTransform(buffer.copy(), chunk), buffer.animate.set_opacity(0), run_time=0.4)
        return chunk, buffer

    def scatter_round(self, chunk_indices):
        """Each encoder takes one chunk off the channel and scatters it."""
        chunks = []
        for chunk_index in chunk_indices:
            chunks.append(self.read_chunk(chunk_index))

        # Down the channel, one chunk per free encoder.
        self.play(*[
            chunk.animate.move_to(self.encoders[slot])
            for slot, (chunk, _) in enumerate(chunks)
        ], run_time=0.8)

        # Encode: lines become uint64 dots coloured by bucket. Anything that
        # is not a well-formed code is dropped.
        flights, drops, dots = [], [], []
        for slot, (chunk, _) in enumerate(chunks):
            source = CHUNKS[chunk_indices[slot]][0]
            for row in chunk[1]:
                code = row.text
                if code not in BUCKET_OF:
                    drops.append(row.animate.set_color(RED).set_opacity(0))
                    continue
                bucket = BUCKET_OF[code]
                dot = Dot(radius=0.07, color=BUCKET_COLOURS[bucket]).move_to(row)
                dot.code, dot.source, dot.bucket = code, source, bucket
                dots.append(dot)
                flights.append((row, dot))
        self.play(
            *drops,
            *[ReplacementTransform(row, dot) for row, dot in flights],
            *[chunk[0].animate.set_stroke(ENCODER_COLOUR) for chunk, _ in chunks],
            run_time=0.7,
        )

        # Append each group to its bucket file under that file's mutex.
        locks = VGroup()
        moves = []
        for dot in dots:
            cell = self.cells[dot.source * 4 + dot.bucket]
            target = cell.get_center() + RIGHT * (len(self.stored[cell]) * 0.16 - 0.24)
            self.stored[cell].append(dot)
            moves.append(dot.animate.move_to(target).scale(0.8))
            if not any(lock.cell is cell for lock in locks):
                lock = label("mu", 10, YELLOW).next_to(cell, DOWN, buff=-0.15).shift(RIGHT * 0.3)
                lock.cell = cell
                locks.add(lock)
        self.play(FadeIn(locks), LaggedStart(*moves, lag_ratio=0.08), run_time=1.4)

        # Release: the empty buffers go back to the pool for the readers.
        returns = []
        for chunk, buffer in chunks:
            returns.append(FadeOut(chunk[0]))
            buffer.set_opacity(1).move_to(chunk[0])
            returns.append(buffer.animate.scale(1.25).move_to(self.pool_slots[self.pool_buffers.submobjects.index(buffer)]))
        self.play(FadeOut(locks), *returns, run_time=0.7)

    def scatter_phase(self):
        self.stored = {cell: [] for cell in self.cells}
        self.pool_slots = [buffer.get_center() for buffer in self.pool_buffers]

        # Readers 0 and 1 take the two semaphore slots; reader 2 waits.
        token0, acquire0 = self.acquire(0, 0)
        token1, acquire1 = self.acquire(1, 1)
        waiting = label("waiting for a slot", 11, GREY_B).next_to(self.readers[2], DOWN, buff=0.08)
        self.play(*acquire0, *acquire1, FadeIn(waiting))

        caption = label("readers fill pooled buffers with whole lines", 16, GREY_B).to_edge(DOWN)
        self.play(FadeIn(caption))
        self.scatter_round([0, 1])

        # Reader 0 reaches the end of its file and frees its slot for reader 2.
        done = label("done", 11, GREY_B).next_to(self.readers[0], DOWN, buff=0.08)
        self.play(
            self.readers[0][0].animate.set_fill(GREY_D, 0.3).set_stroke(GREY_B),
            FadeOut(token0),
            FadeIn(done),
            run_time=0.5,
        )
        token2, acquire2 = self.acquire(2, 0)
        self.play(*acquire2, FadeOut(waiting), run_time=0.5)

        self.play(Transform(caption, label("encode workers pack codes and append them to bucket files", 16, GREY_B).to_edge(DOWN)))
        self.scatter_round([2, 3])

        self.play(Transform(caption, label("every copy of a code lands in the same bucket number", 16, GREY_B).to_edge(DOWN)))
        self.scatter_round([4, 5])
        self.play(FadeOut(caption))
        self.reader_tokens = VGroup(token1, token2)

    # 4. Barrier between the phases

    def barrier(self):
        # Readers 1 and 2 reach the end of their files too.
        self.play(
            *[self.readers[index][0].animate.set_fill(GREY_D, 0.3).set_stroke(GREY_B) for index in (1, 2)],
            *[FadeIn(label("done", 11, GREY_B).next_to(self.readers[index], DOWN, buff=0.08)) for index in (1, 2)],
            FadeOut(self.reader_tokens),
            run_time=0.6,
        )
        close = label("close(chunks)", 16, CHANNEL_COLOUR).next_to(self.chunks_channel, DOWN, buff=0.15)
        self.play(FadeIn(close), self.chunks_channel.animate.set_opacity(0.3))

        steps = label("wg.Wait()  ·  close bucket files  ·  debug.FreeOSMemory()", 20, YELLOW)
        bar = DashedLine(UP * 2.6, DOWN * 2.4, color=YELLOW).next_to(self.grid, LEFT, buff=0.3)
        steps.to_edge(DOWN)
        self.play(Create(bar), FadeIn(steps))
        self.wait(0.8)

        # Everything from the scatter phase goes, except the bucket files.
        leftovers = [mob for mob in self.mobjects if mob not in (self.grid, self.phase, steps, bar, *self.dots_on_grid())]
        self.play(*[FadeOut(mob) for mob in leftovers], FadeOut(bar), run_time=0.8)

        # The grid moves left; the count phase reuses the memory scatter freed.
        everything = VGroup(self.grid, *self.dots_on_grid())
        self.play(
            everything.animate.scale(0.85).move_to(LEFT * 5.0 + UP * 0.25),
            Transform(self.phase, label("Phase 2 · count", 24, YELLOW).to_corner(UL)),
            FadeOut(steps),
        )

    def dots_on_grid(self):
        return [dot for dots in self.stored.values() for dot in dots]

    # 5. Count

    def count_phase(self):
        self.feeder = goroutine_box("main: buckets <- i", GREY_B, width=2.4).move_to(LEFT * 4.6 + DOWN * 2.6)
        bucket_channel = label("buckets (unbuffered)", 13, CHANNEL_COLOUR).next_to(self.feeder, RIGHT, buff=0.4)
        self.counters = VGroup(*[
            goroutine_box(f"count worker {index}", COUNTER_COLOUR, width=2.2) for index in range(2)
        ]).arrange(DOWN, buff=1.6).move_to(LEFT * 0.6 + UP * 0.25)

        # Each worker owns one slot for the whole phase, laid out here as one
        # column per source's part of the bucket.
        self.slots = VGroup(*[
            Rectangle(width=3.8, height=1.1, color=COUNTER_COLOUR, stroke_width=1.5).next_to(counter, RIGHT, buff=0.3)
            for counter in self.counters
        ])
        headers = VGroup(*[
            label(f"src{source}", 11, GREY_B).move_to(self.slot_column(slot, source, -1))
            for slot in self.slots for source in range(3)
        ])
        slot_names = VGroup(*[label("slot", 11, COUNTER_COLOUR).next_to(slot, RIGHT, buff=0.08) for slot in self.slots])

        self.batch = goroutine_box("out.Add → Batch", GREEN_C, width=2.4).to_edge(RIGHT, buff=0.3).shift(DOWN * 2.6)
        self.play(
            GrowFromCenter(self.feeder), FadeIn(bucket_channel),
            LaggedStart(*[GrowFromCenter(counter) for counter in self.counters], lag_ratio=0.2),
            FadeIn(self.slots), FadeIn(headers), FadeIn(slot_names), GrowFromCenter(self.batch),
        )

        self.caption = label("a bucket is handed over only when a worker is free", 16, GREY_B).to_edge(DOWN, buff=0.15)
        self.play(FadeIn(self.caption))
        self.batch_results = []
        self.count_pair([0, 1], explain=True)
        self.play(self.set_caption("both workers count at once; the next buckets wait for a free worker"))
        self.count_pair([2, 3], explain=False)

        self.play(self.set_caption("8 valid codes in the real files; 4 in this toy", GREEN_C))
        self.wait(1.2)
        self.play(*[FadeOut(mob) for mob in self.mobjects])

    def set_caption(self, text, colour=GREY_B):
        return Transform(self.caption, label(text, 16, colour).to_edge(DOWN, buff=0.15))

    def slot_column(self, slot, source, row):
        """Where row (−1 for the header) of source's column in slot sits."""
        width = slot.width / 3
        x = slot.get_left()[0] + width * (source + 0.5)
        y = slot.get_top()[1] - 0.22 - 0.3 * row
        return [x, y, 0]

    def count_pair(self, buckets, explain):
        """Hand one bucket to each worker, then let both count at once."""
        tokens = []
        for worker, bucket in enumerate(buckets):
            # Unbuffered: the send completes only when that worker receives.
            token = label(f"bucket {bucket}", 14, BUCKET_COLOURS[bucket]).move_to(self.feeder.get_top() + UP * 0.25)
            self.play(FadeIn(token, scale=0.5), run_time=0.3)
            self.play(
                token.animate.next_to(self.counters[worker], UP, buff=0.08),
                self.counters[worker][0].animate.set_fill(COUNTER_COLOUR, 0.45),
                run_time=0.6,
            )
            tokens.append(token)

        # Load bucket i of every source into the worker's slot.
        if explain:
            self.play(self.set_caption("load bucket i of every file into the worker's slot"))
        loads, tags = [], []
        for worker, bucket in enumerate(buckets):
            for source in range(3):
                for row, dot in enumerate(self.stored[self.cells[source * 4 + bucket]]):
                    tag = label(dot.code, 12, BUCKET_COLOURS[bucket]).move_to(self.slot_column(self.slots[worker], source, row))
                    tag.code, tag.source, tag.worker = dot.code, source, worker
                    loads.append(ReplacementTransform(dot, tag))
                    tags.append(tag)
        self.play(*loads, run_time=1.2)
        self.wait(0.4)

        # Sort and de-duplicate each part: a repeat within one file counts once.
        if explain:
            self.play(self.set_caption("sort + de-duplicate each file's part: a repeat within one file counts once"))
        seen, repeats = set(), []
        for tag in tags:
            key = (tag.worker, tag.source, tag.code)
            if key in seen:
                repeats.append(tag)
            seen.add(key)
        if repeats:
            self.play(*[tag.animate.set_color(RED) for tag in repeats], run_time=0.5)
            self.play(*[FadeOut(tag, shift=RIGHT * 0.2) for tag in repeats], run_time=0.5)
        kept = [tag for tag in tags if tag not in repeats]

        # k-way merge across the parts: keep codes found in at least 2 files.
        if explain:
            self.play(self.set_caption("k-way merge: keep codes found in at least 2 files"))
        sources_of = {}
        for tag in kept:
            sources_of.setdefault((tag.worker, tag.code), set()).add(tag.source)
        valid = [tag for tag in kept if len(sources_of[(tag.worker, tag.code)]) >= 2]
        self.play(
            *[tag.animate.set_color(GREEN_C) for tag in valid],
            *[tag.animate.set_opacity(0.3) for tag in kept if tag not in valid],
            run_time=0.7,
        )
        self.wait(0.4)

        # Each valid code goes to the batch once.
        results, sent = [], set()
        for tag in valid:
            if (tag.worker, tag.code) in sent:
                continue
            sent.add((tag.worker, tag.code))
            index = len(self.batch_results) + len(results)
            target = self.batch.get_top() + UP * (0.25 + 0.3 * (index // 2)) + RIGHT * (0.6 * (index % 2 * 2 - 1))
            result = tag.copy()
            results.append((result, target))
        self.play(*[result.animate.move_to(target) for result, target in results], run_time=0.9)
        self.batch_results.extend(result for result, _ in results)
        self.play(
            *[FadeOut(tag) for tag in kept],
            *[FadeOut(token) for token in tokens],
            *[counter[0].animate.set_fill(COUNTER_COLOUR, 0.15) for counter in self.counters],
            run_time=0.5,
        )

    # 6. Ending

    def ending_card(self):
        line1 = Text("Memory is fixed by the budget,", font_size=34)
        line2 = Text("not by the size of the input.", font_size=34)
        card = VGroup(line1, line2).arrange(DOWN)
        self.play(Write(card))
        self.wait(1.5)
        self.play(FadeOut(card))
