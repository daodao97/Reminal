class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.0.8"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.8/reminal_2.0.8_darwin_arm64.tar.gz"
      sha256 "b5ed40642509e0299ab4a5632fc5c7334ce6d5e7f751bc1c767e1de72c089346"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.8/reminal_2.0.8_darwin_amd64.tar.gz"
      sha256 "3091ff68e225e4c2337b0796f544f67c4b0d4cffed4f46d0b1c745b5260284b5"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.8/reminal_2.0.8_linux_arm64.tar.gz"
      sha256 "64c3832c4e470624a25dd2feff751accaabdc95f6dc2777dc7309c62dd7483db"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.8/reminal_2.0.8_linux_amd64.tar.gz"
      sha256 "861c6148644e3eb7571bde47c87c7641a29f1ba3a42df190f0c1f2fb89d1fd63"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
      # Build the native window-capture helper from source when Xcode is present;
      # otherwise the window mirror falls back to screencapture.
      if OS.mac? && which("swiftc")
        system "swiftc", "-O", "-o", bin/"reminal-capture", "native/reminal-capture/main.swift"
      end
    else
      bin.install "reminal"
      # The darwin bottle bundles the ScreenCaptureKit capture helper next to the
      # binary; the agent auto-discovers it for the native window mirror.
      bin.install "reminal-capture" if File.exist?("reminal-capture")
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
