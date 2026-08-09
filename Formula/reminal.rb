class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.1.7"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.7/reminal_2.1.7_darwin_arm64.tar.gz"
      sha256 "3ef7cc40d24b97ea291ca70e5f689720f56bddad32aa0de45ca0202a157a949d"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.7/reminal_2.1.7_darwin_amd64.tar.gz"
      sha256 "bb877fad17d02adbc15943ab48b54966f1fd8e9e3e5c926baa8ea1d63cd0a12d"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.7/reminal_2.1.7_linux_arm64.tar.gz"
      sha256 "85061c175cf74909b175cd7c8055b80374820d6eaeed6faa9069b21b3b907ca1"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.7/reminal_2.1.7_linux_amd64.tar.gz"
      sha256 "fea6370b98a451472da6288f4b9441de9d1d1db4bd8d93047c1c1eb16309d12a"
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
