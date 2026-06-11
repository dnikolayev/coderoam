class Coderoam < Formula
  desc "Run each AI coding session in its own mobile group chat"
  homepage "https://github.com/dnikolayev/coderoam"
  url "https://github.com/dnikolayev/coderoam/archive/refs/tags/v0.1.14.tar.gz"
  sha256 "6ace4900257edd1dbdb058640337a0505a3583849f2b7a5bb1f0231cadc2cb41"
  license all_of: ["MIT", "GPL-3.0-only"]

  head "https://github.com/dnikolayev/coderoam.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X github.com/dnikolayev/coderoam/internal/app.version=#{version}"

    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/coderoam"
    system "go", "build", "-o", bin/"coderoam-transcribe", "./cmd/coderoam-transcribe"
    system "go", "build", "-o", bin/"agent-runner", "./examples/agent-runner"
    system "go", "build", "-o", bin/"codex-runner", "./examples/codex-runner"
    system "go", "build", "-o", bin/"claude-runner", "./examples/claude-runner"
    system "go", "build", "-o", bin/"echo-runner", "./examples/echo-runner"
  end

  def caveats
    <<~EOS
      coderoam needs a connected messenger before mobile agent sessions work.

      Start here:
        coderoam setup

      WhatsApp is the implemented transport today:
        coderoam auth login --profile bot --qr

      Voice transcription is optional and requires local tools:
        brew install ffmpeg whisper-cpp
    EOS
  end

  test do
    assert_match "coderoam", shell_output("#{bin}/coderoam version")
    assert_match "Quick WhatsApp setup", shell_output("#{bin}/coderoam setup --print")
  end
end
