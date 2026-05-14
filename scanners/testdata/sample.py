# Intentionally vulnerable fixture for in-image smoke testing.
import subprocess

def run(user_input):
    # Bandit B602 — subprocess.Popen with shell=True
    subprocess.Popen(user_input, shell=True)
