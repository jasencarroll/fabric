import os
import sqlite3

try:
    from flask import Flask, redirect, render_template, request, url_for
except ImportError:
    print("Flask is not installed. Run: uv sync")
    raise SystemExit(1)

app = Flask(__name__)

DB_PATH = os.environ.get("FABRIC_DB", "./fabric.db")


def get_db():
    db = sqlite3.connect(DB_PATH)
    db.row_factory = sqlite3.Row
    return db


with app.app_context():
    db = get_db()
    db.execute(
        "CREATE TABLE IF NOT EXISTS notes "
        "(id INTEGER PRIMARY KEY, text TEXT NOT NULL, "
        "created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)"
    )
    db.commit()
    db.close()


@app.route("/")
def index():
    db = get_db()
    notes = db.execute(
        "SELECT id, text, created_at FROM notes ORDER BY id DESC"
    ).fetchall()
    db.close()
    return render_template("index.html", notes=notes)


@app.route("/add", methods=["POST"])
def add_note():
    text = request.form.get("text", "").strip()
    if text:
        db = get_db()
        db.execute("INSERT INTO notes (text) VALUES (?)", [text])
        db.commit()
        db.close()
    return redirect(url_for("index"))


@app.route("/delete", methods=["POST"])
def delete_note():
    note_id = request.form.get("id", "")
    if note_id.isdigit():
        db = get_db()
        db.execute("DELETE FROM notes WHERE id = ?", [int(note_id)])
        db.commit()
        db.close()
    return redirect(url_for("index"))
